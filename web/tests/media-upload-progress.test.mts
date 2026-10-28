import assert from "node:assert/strict";
import { register } from "node:module";
import test from "node:test";
import { registerSourceResolution } from "./helpers/moved-source.mts";

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
const { uploadDraftMediaAsset } = await import("../src/features/streams/media-asset-client.ts");
const { defaultStreamVisualDraft, buildStreamCreateVisualExtension } = await import("../src/features/streams/stream-visual-draft.ts");
const operationID = "b6cf8101-80f4-4d57-9610-f1a3d41aa245";

test("an unused upload session is retained by the draft but is absent from create payload", () => {
  const draft = { ...defaultStreamVisualDraft(), uploadSessionID: "owned-unused-session" };
  assert.equal(Object.hasOwn(buildStreamCreateVisualExtension(draft), "upload_session_id"), false);
  assert.equal(draft.uploadSessionID, "owned-unused-session");
  draft.coverCandidates = [{ name: "shared.png", source: "preset", preset_id: "shared-preset", asset_id: "shared-original", variant_id: "shared-variant" }];
  assert.equal(Object.hasOwn(buildStreamCreateVisualExtension(draft), "upload_session_id"), false);
});

test("an actual uploaded candidate or background still requires its exact draft session", () => {
  const draft = { ...defaultStreamVisualDraft(), uploadSessionID: "owned-selected-session" };
  draft.coverCandidates = [{ name: "unselected.png", source: "upload", asset_id: "owned-original", variant_id: "owned-variant" }];
  assert.equal(buildStreamCreateVisualExtension(draft).upload_session_id, "owned-selected-session");
  draft.coverCandidates = [];
  draft.backgroundMode = "image";
  draft.backgroundAssetID = "owned-background";
  draft.backgroundVariantID = "background-variant";
  assert.equal(buildStreamCreateVisualExtension(draft).upload_session_id, "owned-selected-session");
});

function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => { resolve = done; });
  return { promise, resolve };
}

async function withUploadFixture(body: (control: { entered: ReturnType<typeof deferred>; release: ReturnType<typeof deferred> }) => Promise<void>) {
  const originalFetch = globalThis.fetch, originalDemo = process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO;
  process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO = "false";
  const entered = deferred(), release = deferred();
  globalThis.fetch = async (input, init) => {
    const path = String(input);
    if (path === "/media-assets/upload-sessions") return Response.json({ id: "stage-session", owner_type: "upload_draft", expires_at: "2099-01-01T00:00:00Z" });
    if (path === "/media-assets") {
      assert.equal(init?.method, "POST");
      assert.ok(init.body instanceof FormData);
      return Response.json({ id: "stage-asset", owner_type: "upload_draft", owner_id: "stage-session", upload_session_id: "stage-session", client_operation_id: operationID, usage_type: "video_cover", media_type: "image/png", byte_size: 128, width: 640, height: 360, opaque: true, sha256: "a".repeat(64) });
    }
    if (path === "/media-assets/stage-asset/variants") {
      entered.resolve();
      await release.promise;
      return Response.json({ id: "stage-variant", asset_id: "stage-asset", status: "ready", width: 1920, height: 1080, opaque: true });
    }
    throw new Error("Unexpected request in progress fixture: " + path);
  };
  try { await body({ entered, release }); }
  finally {
    release.resolve();
    globalThis.fetch = originalFetch;
    if (originalDemo === undefined) delete process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO;
    else process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO = originalDemo;
  }
}

test("each upload reports actual preparation, upload, conversion and readiness in order", async () => {
  await withUploadFixture(async ({ release }) => {
    const phases: string[] = [];
    release.resolve();
    const result = await uploadDraftMediaAsset(new File(["controlled image bytes"], "second-cover.png", { type: "image/png" }), "video_cover", undefined, {
      operationID, onProgress: (event: { phase: string }) => phases.push(event.phase),
    });
    assert.equal(result.asset.id, "stage-asset");
    assert.deepEqual(phases.filter((phase, index) => index === 0 || phases[index - 1] !== phase), ["preparing", "uploading", "converting", "ready"]);
  });
});

test("a delayed variant never reports readiness while its actual response is pending", async () => {
  await withUploadFixture(async ({ entered, release }) => {
    const phases: string[] = [];
    const pending = uploadDraftMediaAsset(new File(["controlled image bytes"], "fourth-cover.png", { type: "image/png" }), "video_cover", undefined, {
      operationID, onProgress: (event: { phase: string }) => phases.push(event.phase),
    });
    try {
      await entered.promise;
      assert.equal(phases.at(-1), "converting");
      assert.equal(phases.includes("ready"), false);
    } finally { release.resolve(); await pending; }
    assert.equal(phases.at(-1), "ready");
  });
});
