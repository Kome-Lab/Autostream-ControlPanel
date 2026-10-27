import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { register } from "node:module";
import test from "node:test";

const resolverSource = [
  "let webRootURL;",
  "export function initialize(data) { webRootURL = data.webRootURL; }",
  "export async function resolve(specifier, context, nextResolve) {",
  "  if (specifier.startsWith('@/')) {",
  "    const target = new URL('src/' + specifier.slice(2), webRootURL);",
  "    if (!/\\.[cm]?[jt]sx?$/.test(target.pathname)) target.pathname += '.ts';",
  "    return nextResolve(target.href, context);",
  "  }",
  "  return nextResolve(specifier, context);",
  "}",
].join("\n");

register(`data:text/javascript,${encodeURIComponent(resolverSource)}`, {
  parentURL: import.meta.url,
  data: { webRootURL: new URL("../", import.meta.url).href },
});

const {
  buildStreamCreateVisualExtension,
  buildStreamVisualFields,
  coverCandidatePreviewURL,
  createStreamVisualPreviewOwner,
  defaultStreamVisualDraft,
  uploadedCoverCount,
  validateStreamVisualDraft,
  visualCapabilityWarnings,
} = await import("../src/features/streams/stream-visual-draft.ts");
const { coverActionAvailability } = await import("../src/features/streams/control-platform.ts");
const {
  createStreamVisualActionController,
} = await import("../src/features/streams/stream-visual-action-controller.ts");

const editorSource = readFileSync(new URL("../src/features/streams/stream-visual-settings-section.tsx", import.meta.url), "utf8");
const panelSource = readFileSync(new URL("../src/features/streams/stream-control-platform-panel.tsx", import.meta.url), "utf8");
const { mediaAssetBelongsToDraft } = await import("../src/features/streams/media-asset-status.ts");
const { adaptAPIError } = await import("../src/lib/foundation/api-errors/adapter.ts");

test("new preset candidates use their preset authority until the stream snapshot is saved", () => {
  const candidate = { name: "Shared", source: "preset" as const, preset_id: "preset-one", preset_revision: 7, asset_id: "shared-asset", variant_id: "shared-variant" };
  const saved = { stream_id: "stream-one", cover_source: "none", cover_candidates: [], cover_asset_id: "", cover_variant_id: "" } as unknown as import("../src/features/streams/control-platform.ts").StreamVisualSettings;
  assert.equal(coverCandidatePreviewURL(candidate, "stream-one", saved), "/video-cover-presets/preset-one/media-assets/shared-variant");
  assert.equal(coverCandidatePreviewURL(candidate, "stream-one", { ...saved, cover_candidates: [candidate] }), "/streams/stream-one/media-assets/shared-variant");
  assert.equal(coverCandidatePreviewURL(candidate, "stream-one", { ...saved, cover_asset_id: candidate.asset_id, cover_variant_id: candidate.variant_id }), "/streams/stream-one/media-assets/shared-variant");
  assert.equal(coverCandidatePreviewURL(candidate, "stream-one", saved, "blob:local-image"), "blob:local-image");
  assert.equal(coverCandidatePreviewURL({ ...candidate, source: "upload", preset_id: undefined }, "stream-one", saved), "", "unclaimed uploads must never select a stream preview route");
});

test("draft image cleanup requires the exact upload owner and session", () => {
  const asset = { id: "asset", usage_type: "video_cover" as const, media_type: "image/png", width: 1920, height: 1080, sha256: "test", owner_type: "upload_draft", owner_id: "draft-one", upload_session_id: "draft-one" };
  assert.equal(mediaAssetBelongsToDraft(asset, "draft-one"), true);
  for (const changed of [{ owner_type: "stream" }, { owner_id: "other-draft" }, { upload_session_id: "other-draft" }, { owner_type: undefined }]) {
    assert.equal(mediaAssetBelongsToDraft({ ...asset, ...changed }, "draft-one"), false);
  }
  assert.equal(mediaAssetBelongsToDraft(asset, ""), false);
  assert.equal(mediaAssetBelongsToDraft(asset, "other-draft"), false);
});

test("the server upload limit has a specific reason while authorization remains authoritative", () => {
  const error = { name: "APIError", status: 409, code: "cover_upload_limit" };
  assert.equal(adaptAPIError(error).messageKey, "apiErrorCoverUploadLimit");
  assert.equal(adaptAPIError({ ...error, status: 403 }).kind, "forbidden");
});

test("cover upload allowance counts original uploaded assets independently of presets and variants", () => {
  const candidates = [
    { name: "first.png", source: "upload" as const, asset_id: "asset-one", variant_id: "variant-one" },
    { name: "first-small.png", source: "upload" as const, asset_id: "asset-one", variant_id: "variant-two" },
    { name: "preset", source: "preset" as const, asset_id: "asset-shared", variant_id: "variant-preset" },
    ...[2, 3, 4, 5].map((id) => ({ name: `${id}.png`, source: "upload" as const, asset_id: `asset-${id}`, variant_id: `variant-${id}` })),
  ];
  assert.equal(uploadedCoverCount(candidates), 5);
  assert.equal(uploadedCoverCount(candidates.filter((candidate) => candidate.asset_id !== "asset-5")), 4);
  assert.equal(uploadedCoverCount(candidates.filter((candidate) => candidate.source === "preset")), 0);
  const draft = defaultStreamVisualDraft();
  draft.coverCandidates = candidates;
  assert.equal(validateStreamVisualDraft(draft).ready, true, "legacy sets remain editable without implicit truncation");
  assert.deepEqual(buildStreamVisualFields(draft).cover_candidates, candidates);
});

test("default create sends the complete v2 visual snapshot and edits retain explicit defaults", () => {
  const draft = defaultStreamVisualDraft();
  assert.deepEqual(buildStreamCreateVisualExtension(draft), {
    visual_settings: {
      background_mode: "default",
      background_asset_id: null,
      background_variant_id: null,
      header_title_mode: "default",
      header_title_value: null,
      discord_target: { mode: "inherit" },
      cover_source: "none",
      cover_preset_id: null,
      cover_asset_id: null,
      cover_variant_id: null,
      cover_start_active: false,
 cover_candidates:[],
    },
  });
  draft.headerTitleMode = "custom";
  draft.headerTitleValue = "Program title";
  const extension = buildStreamCreateVisualExtension(draft);
  assert.deepEqual(extension, {
    visual_settings: {
      background_mode: "default",
      background_asset_id: null,
      background_variant_id: null,
      header_title_mode: "custom",
      header_title_value: "Program title",
      discord_target: { mode: "inherit" },
      cover_source: "none",
      cover_preset_id: null,
      cover_asset_id: null,
      cover_variant_id: null,
      cover_start_active: false,
 cover_candidates:[],
    },
  });
});

test("visual fields emit only selected Discord and Cover modes", () => {
  const draft = defaultStreamVisualDraft();
  Object.assign(draft, {
    discordTargetMode: "preset",
    discordTargetPresetID: "discord-preset-1",
    discordTargetPresetRevision: 8,
    discordGuildID: "must-not-leak",
    coverSource: "preset",
    coverPresetID: "cover-preset-1",
    coverAssetID: "client-must-not-resolve",
    coverVariantID: "client-must-not-resolve",
    coverStartActive: true,
  });
  const fields = buildStreamVisualFields(draft, new Set(["discord", "cover"]));
  assert.deepEqual(fields, {
    discord_target: {
      mode: "preset",
      preset_id: "discord-preset-1",
      preset_revision: 8,
    },
    cover_source: "preset",
    cover_preset_id: "cover-preset-1",
    cover_start_active: true,
 cover_variant_id:"client-must-not-resolve",
 cover_candidates:[],
  });
});

test("draft validation rejects unsafe titles and incomplete manual or upload state", () => {
  const draft = defaultStreamVisualDraft();
  Object.assign(draft, { headerTitleMode: "custom", headerTitleValue: "unsafe\u202etitle" });
  assert.equal(validateStreamVisualDraft(draft).ready, false);
  Object.assign(draft, { headerTitleValue: "Safe title", discordTargetMode: "manual", discordGuildID: "1", discordTextChannelID: "", discordVoiceChannelID: "3" });
  assert.equal(validateStreamVisualDraft(draft).ready, false);
  Object.assign(draft, { discordTextChannelID: "2", coverSource: "upload", coverAssetID: "asset", coverVariantID: "" });
  assert.equal(validateStreamVisualDraft(draft).ready, false);
  draft.coverVariantID = "variant";
  assert.equal(validateStreamVisualDraft(draft).ready, true);
});

test("custom scene and Cover expose capability-blocked readiness without fallback", () => {
  const draft = defaultStreamVisualDraft();
  Object.assign(draft, {
    backgroundMode: "image", backgroundAssetID: "asset-bg", backgroundVariantID: "variant-bg",
    coverSource: "preset", coverPresetID: "cover-preset",
  });
  const warnings = visualCapabilityWarnings(draft, { sceneAppearance: false, videoCover: false });
  assert.equal(warnings.length, 2);
  assert.match(warnings[0], /scene_appearance_v1/);
  assert.match(warnings[1], /live_video_cover_v1/);
});

test("visual settings controller revalidates authority and latches failures without resend", async () => {
  let stateCalls = 0;
  let requests = 0;
  const changed = createStreamVisualActionController({
    getPermission: () => ({ kind: "ready", permissions: ["streams.update"] }),
    getState: () => ({ kind: "ready", freshness: "fresh", revision: stateCalls++ < 2 ? 1 : 2, fingerprint: stateCalls < 3 ? "r1" : "r2" }),
    mutate: async () => { requests++; return {} as never; },
  });
  assert.equal((await changed.issue({ header_title_mode: "default" }, 1)).kind, "blocked");
  assert.equal(requests, 0);

  const conflict = createStreamVisualActionController({
    getPermission: () => ({ kind: "ready", permissions: ["*"] }),
    getState: () => ({ kind: "ready", freshness: "fresh", revision: 4, fingerprint: "r4" }),
    mutate: async () => { requests++; throw Object.assign(new Error(), { name: "APIError", status: 409, code: "revision_conflict" }); },
  });
  assert.equal((await conflict.issue({ header_title_mode: "default" }, 4)).kind, "failed");
  assert.equal((await conflict.issue({ header_title_mode: "default" }, 4)).kind, "blocked");
  assert.equal(requests, 1);
  conflict.reconcile();
  assert.equal(conflict.unresolved, false);
});

test("visual save never rebases an older displayed draft onto a newer cache revision", async () => {
  let requests = 0;
  const controller = createStreamVisualActionController({
    getPermission: () => ({ kind: "ready", permissions: ["streams.update"] }),
    getState: () => ({ kind: "ready", freshness: "fresh", revision: 5, fingerprint: "cache-r5" }),
    mutate: async () => { requests++; return {} as never; },
  });
  assert.deepEqual(await controller.issue({ header_title_mode: "custom", header_title_value: "Draft from r4" }, 4), { kind: "blocked", reason: "authority-changed" });
  assert.equal(requests, 0, "a cache update before React renders must not authorize the older draft");
});

test("visual save sends its fixed revision, including a new legacy default at revision zero", async () => {
  const requests: Record<string, unknown>[] = [];
  for (const revision of [0, 4]) {
    const controller = createStreamVisualActionController({
      getPermission: () => ({ kind: "ready", permissions: ["streams.update"] }),
      getState: () => ({ kind: "ready", freshness: "fresh", revision, fingerprint: `r${revision}` }),
      mutate: async (request) => { requests.push(request); return {} as never; },
    });
    assert.equal((await controller.issue({ header_title_mode: "default", expected_revision: 99 }, revision)).kind, "succeeded");
    assert.equal(requests.at(-1)?.expected_revision, revision);
    for (const invalid of [-1, 0.5, Number.NaN, Number.MAX_SAFE_INTEGER + 1]) {
      assert.deepEqual(await controller.issue({ header_title_mode: "default" }, invalid), { kind: "blocked", reason: "state-unavailable" });
    }
  }
  assert.equal(requests.length, 2);
});

test("Cover UI reconciles by GET only, separates desired/applied, and keeps Watermark topmost", () => {
  assert.doesNotMatch(panelSource, /action\.mutate\(lastAction\)/);
  assert.match(panelSource, /cover\.refetch\(\)/);
  assert.match(panelSource, /cover\.data\?\.applied_active && cover\.data\.applied_asset_variant_id/);
  assert.doesNotMatch(panelSource, /desired_active[^\n]+data-video-cover="applied"/);
  assert.match(panelSource, /data-watermark-layer="topmost"/);
  assert.match(panelSource, /Watermark/);assert.match(panelSource,/反映確認/);
  assert.match(panelSource, /live_video_cover_v1/);
});

test("visual editor accepts local image files only and never exposes storage authority", () => {
  assert.match(editorSource, /accept="image\/png,image\/jpeg,image\/webp"/);
  assert.match(editorSource, /backgroundSize: "cover"/);
  assert.match(editorSource, /backgroundPosition: "center"/);
  assert.doesNotMatch(editorSource, /storage_key|https?:\/\/.*image|external.*url/i);
});

test("background and Cover preview URLs keep independent ownership in both replacement orders", () => {
  const revokedForward: string[] = [];
  const forward = createStreamVisualPreviewOwner((url: string) => revokedForward.push(url));
  forward.replace("background", "blob:background-1");
  forward.replace("cover", "blob:cover-1");
  assert.deepEqual(revokedForward, []);
  forward.replace("background", "blob:background-2");
  assert.deepEqual(revokedForward, ["blob:background-1"]);
  forward.release();
  assert.deepEqual(new Set(revokedForward), new Set(["blob:background-1", "blob:background-2", "blob:cover-1"]));

  const revokedReverse: string[] = [];
  const reverse = createStreamVisualPreviewOwner((url: string) => revokedReverse.push(url));
  reverse.replace("cover", "blob:cover-2");
  reverse.replace("background", "blob:background-3");
  assert.deepEqual(revokedReverse, []);
  reverse.replace("cover", "blob:cover-3");
  assert.deepEqual(revokedReverse, ["blob:cover-2"]);
  reverse.release();
  assert.deepEqual(new Set(revokedReverse), new Set(["blob:cover-2", "blob:cover-3", "blob:background-3"]));
});

test("asymmetric Cover permissions expose distinct Show and Hide reasons to assistive technology", () => {
  const permission = { status: "ready" as const, permissions: ["streams.show_cover"] };
  const capability = { status: "ready" as const, supported: true };
  assert.equal(coverActionAvailability(true, permission, false, capability).allowed, true);
  const hide = coverActionAvailability(false, permission, false, capability);
  assert.equal(hide.allowed, false);
  assert.match(hide.reason, /streams\.hide_cover/);
  assert.match(panelSource, /aria-describedby=\{showReason \? showReasonID : undefined\}/);
  assert.match(panelSource, /aria-describedby=\{hideReason \? hideReasonID : undefined\}/);
  assert.match(panelSource, /id=\{showReasonID\}/);
  assert.match(panelSource, /id=\{hideReasonID\}/);
});
