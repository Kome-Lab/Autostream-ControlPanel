import assert from "node:assert/strict";
import test from "node:test";
import { registerSourceResolution } from "./helpers/moved-source.mts";
import type { MediaUploadOwnerOptions, MediaUploadTransport } from "../src/features/streams/media-upload-owner.ts";
import type { DraftMediaCheck, MediaUploadOptions, ReadyDraftMedia } from "../src/features/streams/media-upload-protocol.ts";

registerSourceResolution();
const { createMediaUploadOwner } = await import("../src/features/streams/media-upload-owner.ts");
const session = { id: "draft-one", owner_type: "upload_draft", expires_at: "2099-01-01T00:00:00Z" };
function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}
function fixture(count = 0) {
  type Request = { file: File; options: MediaUploadOptions; usage: "video_cover" | "scene_background"; task: ReturnType<typeof deferred<ReadyDraftMedia>> };
  let serial = 0, allowed = true, covers = count;
  const requests: Request[] = [], adopted: ReadyDraftMedia[] = [], revoked: string[] = [], discarded: string[] = [];
  const checks: string[] = [], conversions: string[] = [];
  let checkResult: DraftMediaCheck = { kind: "missing", session };
  let discardError: Error | undefined;
  const transport: MediaUploadTransport = {
    upload: (file, usage, _session, options) => { const task = deferred<ReadyDraftMedia>(); requests.push({ file, usage, options, task }); return task.promise; },
    check: async (_session, operationID) => { checks.push(operationID); return checkResult; },
    convert: async (_session, asset) => { conversions.push(asset.id); return { session, asset, variant: { id: "variant-resumed", asset_id: asset.id, status: "ready", width: 1920, height: 1080, opaque: true } }; },
    discard: async (asset, sessionID) => { discarded.push(`${sessionID}/${asset.id}`); if (discardError) throw discardError; },
    createURL: () => `blob:preview-${++serial}`, revokeURL: url => revoked.push(url),
    operationID: () => `00000000-0000-4000-8000-${String(++serial).padStart(12, "0")}`,
  };
  const settings: MediaUploadOwnerOptions = { ownerKey: "create-one", sessionID: "", coverCount: covers, candidateCount: covers,
    canStart: () => allowed, onReady: (kind, value) => { adopted.push(value); if (kind === "cover") covers++; owner.configure({ ...settings, sessionID: session.id, coverCount: covers, candidateCount: covers }); } };
  const owner = createMediaUploadOwner(settings, transport);
  function ready(operationID: string, name = "one", usage: "video_cover" | "scene_background" = "video_cover"): ReadyDraftMedia {
    const asset = { id: `asset-${name}`, owner_type: "upload_draft", owner_id: session.id, upload_session_id: session.id, client_operation_id: operationID, usage_type: usage,
      media_type: "image/png", width: 640, height: 360, sha256: "a".repeat(64) };
    return { session, asset, variant: { id: `variant-${name}`, asset_id: asset.id, status: "ready", width: 1920, height: 1080, opaque: true } };
  }
  const finish = (index = requests.length - 1) => { const request = requests[index]; request.task.resolve(ready(request.options.operationID!, String(index), request.usage)); };
  const event = (index: number, phase: "uploading" | "converting" | "outcome_unknown" | "failed", cleanupConfirmed = false) => {
    const request = requests[index], value = ready(request.options.operationID!, String(index), request.usage);
    request.options.onProgress?.({ phase, operationID: request.options.operationID!, usage: request.usage, session, asset: value.asset, retryable: phase === "failed", cleanupConfirmed });
  };
  return { owner, requests, adopted, revoked, discarded, checks, conversions, ready, finish, event,
    setAllowed: (value: boolean) => { allowed = value; }, setCheck: (value: DraftMediaCheck) => { checkResult = value; },
    setDiscardError: (value?: Error) => { discardError = value; } };
}
const file = (name = "cover.png") => new File(["fixture bytes"], name, { type: "image/png" });

test("pending cards exist immediately, stay serial and adopt only an actually ready result", async () => {
  const flow = fixture(), pending = flow.owner.start("cover", file("second.png"));
  assert.equal(flow.owner.getSnapshot()?.name, "second.png");
  assert.equal(flow.owner.getSnapshot()?.phase, "preparing");
  assert.equal(flow.owner.isBlocked(), true);
  assert.equal(flow.adopted.length, 0);
  await flow.owner.start("cover", file("ignored.png"));
  assert.equal(flow.requests.length, 1);
  flow.event(0, "uploading"); assert.equal(flow.owner.getSnapshot()?.phase, "uploading");
  flow.event(0, "converting"); assert.equal(flow.owner.getSnapshot()?.phase, "converting");
  assert.equal(flow.adopted.length, 0);
  flow.finish(); await pending;
  assert.equal(flow.adopted.length, 1); assert.equal(flow.owner.isBlocked(), false);
  assert.deepEqual(flow.revoked, [], "a ready preview transfers to the candidate owner");
});

test("four distinct ready cover originals and a background stay independent; legacy five cannot add", async () => {
  const flow = fixture();
  for (let index = 0; index < 4; index++) {
    const pending = flow.owner.start("cover", file(`cover-${index}.png`));
    assert.equal(flow.requests.length, index + 1); flow.finish(); await pending;
  }
  await flow.owner.start("cover", file("fifth.png")); assert.equal(flow.requests.length, 4);
  const background = flow.owner.start("background", file("background.png"));
  assert.equal(flow.requests.length, 5); flow.finish(); await background;
  assert.equal(flow.adopted.length, 5);
  const legacy = fixture(5); await legacy.owner.start("cover", file()); assert.equal(legacy.requests.length, 0);
});

test("cancelled selection and a forbidden start do not allocate a preview or operation", async () => {
  const flow = fixture(); await flow.owner.start("cover"); flow.setAllowed(false); await flow.owner.start("cover", file());
  assert.equal(flow.owner.getSnapshot(), undefined); assert.equal(flow.requests.length, 0); assert.deepEqual(flow.revoked, []);
});

test("overlong code-point filenames stay visible as failed and never send a body", async () => {
  const flow = fixture(); await flow.owner.start("cover", file("画".repeat(256)));
  assert.equal(flow.owner.getSnapshot()?.issue, "file_name"); assert.equal(flow.owner.getSnapshot()?.cleanupConfirmed, true);
  assert.equal(flow.requests.length, 0); assert.equal(flow.owner.isBlocked(), true);
  await flow.owner.dismiss(); assert.equal(flow.owner.isBlocked(), false); assert.equal(flow.revoked.length, 1);
});

test("reset aborts only its generation, revokes its blob and rejects a late ready completion", async () => {
  const flow = fixture(), pending = flow.owner.start("cover", file());
  const signal = flow.requests[0].options.signal;
  flow.owner.reset(); assert.equal(signal?.aborted, true); assert.equal(flow.revoked.length, 1);
  flow.finish(); await pending;
  assert.equal(flow.adopted.length, 0); assert.deepEqual(flow.discarded, ["draft-one/asset-0"]);
  assert.equal(flow.owner.getSnapshot(), undefined);
});

test("unmount followed by activation cannot adopt a stale result into the next owner", async () => {
  const flow = fixture(), pending = flow.owner.start("background", file());
  flow.owner.dispose(); flow.owner.activate(); flow.finish(); await pending;
  assert.equal(flow.adopted.length, 0); assert.equal(flow.revoked.length, 1); assert.equal(flow.discarded.length, 1);
});

test("an unknown upload is reconciled by GET only and missing/processing never trigger another upload", async () => {
  const flow = fixture(), pending = flow.owner.start("cover", file());
  flow.event(0, "outcome_unknown"); flow.requests[0].task.reject(new Error("lost response")); await pending;
  await flow.owner.check(); assert.equal(flow.owner.getSnapshot()?.phase, "outcome_unknown");
  const value = flow.ready(flow.requests[0].options.operationID!);
  flow.setCheck({ kind: "processing", session, asset: value.asset, variant: { ...value.variant, status: "processing" } });
  await flow.owner.check(); assert.equal(flow.owner.getSnapshot()?.canResume, false);
  assert.equal(flow.requests.length, 1); assert.equal(flow.conversions.length, 0); assert.equal(flow.discarded.length, 0);
  flow.setCheck({ kind: "ready", ...value }); await flow.owner.check();
  assert.equal(flow.adopted.length, 1); assert.equal(flow.owner.isBlocked(), false); assert.equal(flow.requests.length, 1);
});

test("only an explicit resume converts the same confirmed original, without another upload", async () => {
  const flow = fixture(), pending = flow.owner.start("cover", file());
  flow.event(0, "outcome_unknown"); flow.requests[0].task.reject(new Error("lost variant")); await pending;
  const value = flow.ready(flow.requests[0].options.operationID!);
  flow.setCheck({ kind: "uploaded", session, asset: value.asset }); await flow.owner.check();
  assert.equal(flow.owner.getSnapshot()?.canResume, true); assert.equal(flow.conversions.length, 0);
  await flow.owner.resume(); assert.deepEqual(flow.conversions, [value.asset.id]); assert.equal(flow.requests.length, 1);
});

test("a failed original is cleaned with its exact session before an explicit new-operation retry", async () => {
  const flow = fixture(), pending = flow.owner.start("cover", file());
  flow.event(0, "failed"); flow.requests[0].task.reject(new Error("failed variant")); await pending;
  assert.equal(flow.owner.isBlocked(), true); assert.equal(flow.requests.length, 1);
  const oldID = flow.requests[0].options.operationID, retry = flow.owner.retry();
  await new Promise<void>(resolve => setImmediate(resolve));
  assert.deepEqual(flow.discarded, ["draft-one/asset-0"]); assert.equal(flow.requests.length, 2);
  assert.notEqual(flow.requests[1].options.operationID, oldID);
  flow.finish(); await retry; assert.equal(flow.adopted.length, 1);
});

test("a lost cleanup receipt blocks retry until a read-only tombstone proves removal", async () => {
  const flow = fixture(), pending = flow.owner.start("cover", file());
  flow.event(0, "failed"); flow.requests[0].task.reject(new Error("failed variant")); await pending;
  flow.setDiscardError(new Error("delete response lost")); await flow.owner.retry();
  assert.equal(flow.owner.getSnapshot()?.issue, "cleanup_unknown"); assert.equal(flow.requests.length, 1);
  await flow.owner.retry(); assert.equal(flow.discarded.length, 1, "an uncertain delete is not repeated");
  flow.setCheck({ kind: "deleted", session, asset: { ...flow.ready(flow.requests[0].options.operationID!).asset, deleted_at: "2026-10-28T00:00:00Z" } });
  await flow.owner.check(); assert.equal(flow.owner.getSnapshot()?.cleanupConfirmed, true);
  await flow.owner.dismiss(); assert.equal(flow.owner.isBlocked(), false); assert.equal(flow.discarded.length, 1);
});

test("claimed and expired session observations retain the draft and never adopt, convert or delete", async () => {
  for (const kind of ["claimed", "expired"] as const) {
    const flow = fixture(), pending = flow.owner.start("cover", file());
    flow.event(0, "outcome_unknown"); flow.requests[0].task.reject(new Error("lost response")); await pending;
    flow.setCheck({ kind, session }); await flow.owner.check(); await flow.owner.resume(); await flow.owner.dismiss();
    assert.equal(flow.owner.getSnapshot()?.issue, kind); assert.equal(flow.adopted.length, 0); assert.equal(flow.discarded.length, 0); assert.equal(flow.conversions.length, 0);
  }
});
