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
  createStreamVisualPreviewOwner,
  defaultStreamVisualDraft,
  validateStreamVisualDraft,
  visualCapabilityWarnings,
} = await import("../src/features/streams/stream-visual-draft.ts");
const { coverActionAvailability } = await import("../src/features/streams/control-platform.ts");
const {
  createStreamVisualActionController,
} = await import("../src/features/streams/stream-visual-action-controller.ts");

const editorSource = readFileSync(new URL("../src/features/streams/stream-visual-settings-section.tsx", import.meta.url), "utf8");
const panelSource = readFileSync(new URL("../src/features/streams/stream-control-platform-panel.tsx", import.meta.url), "utf8");

test("default create preserves the legacy stream path and partial visual edits stay partial", () => {
  const draft = defaultStreamVisualDraft();
  assert.deepEqual(buildStreamCreateVisualExtension(draft, new Set()), {});
  draft.headerTitleMode = "custom";
  draft.headerTitleValue = "Program title";
  const extension = buildStreamCreateVisualExtension(draft, new Set(["title"]));
  assert.deepEqual(extension, {
    visual_settings: { header_title_mode: "custom", header_title_value: "Program title" },
  });
  assert.equal("discord_target_mode" in extension.visual_settings, false);
  assert.equal("background_mode" in extension.visual_settings, false);
  assert.equal("cover_source" in extension.visual_settings, false);
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
    discord_target_mode: "preset",
    discord_target_preset_id: "discord-preset-1",
    discord_target_preset_revision: 8,
    cover_source: "preset",
    cover_preset_id: "cover-preset-1",
    cover_start_active: true,
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
  assert.equal((await changed.issue({ header_title_mode: "default" })).kind, "blocked");
  assert.equal(requests, 0);

  const conflict = createStreamVisualActionController({
    getPermission: () => ({ kind: "ready", permissions: ["*"] }),
    getState: () => ({ kind: "ready", freshness: "fresh", revision: 4, fingerprint: "r4" }),
    mutate: async () => { requests++; throw Object.assign(new Error(), { name: "APIError", status: 409, code: "revision_conflict" }); },
  });
  assert.equal((await conflict.issue({ header_title_mode: "default" })).kind, "failed");
  assert.equal((await conflict.issue({ header_title_mode: "default" })).kind, "blocked");
  assert.equal(requests, 1);
  conflict.reconcile();
  assert.equal(conflict.unresolved, false);
});

test("Cover UI reconciles by GET only, separates desired/applied, and keeps Watermark topmost", () => {
  assert.doesNotMatch(panelSource, /action\.mutate\(lastAction\)/);
  assert.match(panelSource, /cover\.refetch\(\)/);
  assert.match(panelSource, /cover\.data\?\.applied_active === true/);
  assert.doesNotMatch(panelSource, /desired_active[^\n]+data-video-cover="applied"/);
  assert.match(panelSource, /data-watermark-layer="topmost"/);
  assert.match(panelSource, /Watermark.*revision/);
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
