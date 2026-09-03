import assert from "node:assert/strict";
import test from "node:test";

import {
  applyThemeToRoot,
  buildUIPreferenceUpdate,
  legacyThemeMigrationMode,
  legacyThemeStorageKey,
  readThemeMirror,
  safeUserUIPreference,
  semanticStatusTokenNames,
  themeMirrorStorageKey,
  userColorModes,
  userThemeIDs,
} from "../src/features/account/ui-preferences.ts";
import {
  buildDiscordTargetPresetPayload,
  validDiscordOpaqueID,
  validDiscordTargetPreset,
} from "../src/features/resources/discord-target-preset.ts";
import {
  buildDiscordTargetPayload,
  buildVideoCoverAction,
  buildVisualUpdate,
  createVideoCoverActionController,
  newVideoCoverIdempotencyKey,
  visualPresentation,
  type CoverPermissionSnapshot,
  type VideoCoverState,
} from "../src/features/streams/control-platform.ts";
import { requireReadyMediaVariant, type MediaAsset, type MediaAssetVariant } from "../src/features/streams/media-asset-status.ts";

test("12 themes x 3 modes validate and apply without redefining semantic status meaning", () => {
  assert.equal(userThemeIDs.length, 12);
  assert.equal(userColorModes.length, 3);
  assert.deepEqual(semanticStatusTokenNames, ["live", "running", "healthy", "warning", "critical", "offline", "pending", "completed", "disabled"]);
  let combinations = 0;
  for (const themeID of userThemeIDs) {
    for (const colorMode of userColorModes) {
      const preference = safeUserUIPreference({ theme_id: themeID, color_mode: colorMode, revision: 7 });
      assert.equal(preference.fallback, false);
      const classes = new Set<string>();
      const root = {
        dataset: {} as DOMStringMap,
        classList: { toggle: (name: string, enabled?: boolean) => { if (enabled) classes.add(name); else classes.delete(name); return Boolean(enabled); } },
      } as unknown as HTMLElement;
      applyThemeToRoot(root, preference, true);
      assert.equal(root.dataset.theme, themeID);
      assert.equal(root.dataset.colorMode, colorMode);
      assert.equal(classes.has("dark"), colorMode !== "light");
      assert.deepEqual(buildUIPreferenceUpdate(preference), { theme_id: themeID, color_mode: colorMode, expected_revision: 7 });
      combinations++;
    }
  }
  assert.equal(combinations, 36);
});

test("theme bootstrap uses a non-secret mirror, migrates legacy mode, and safely renders unknown DB values", () => {
  const values = new Map<string, string>([[legacyThemeStorageKey, "dark"]]);
  const storage = { getItem: (key: string) => values.get(key) ?? null } as Pick<Storage, "getItem">;
  assert.deepEqual(readThemeMirror(storage), safeUserUIPreference({ theme_id: "autostream", color_mode: "dark", revision: 0 }));
	assert.equal(legacyThemeMigrationMode(storage), "dark");
  values.set(themeMirrorStorageKey, JSON.stringify({ theme_id: "ocean", color_mode: "light" }));
  assert.equal(readThemeMirror(storage).theme_id, "ocean");
	assert.equal(legacyThemeMigrationMode(storage), null, "new bootstrap mirror must suppress legacy DB migration");
  const unknown = safeUserUIPreference({ theme_id: "future-theme", color_mode: "infrared", revision: 19 });
  assert.equal(unknown.theme_id, "autostream");
  assert.equal(unknown.color_mode, "system");
  assert.equal(unknown.revision, 19, "safe rendering must not fabricate a DB rewrite revision");
  assert.equal(unknown.fallback, true);
});

test("media asset status accepts only a verified ready variant and exposes safe failure codes", () => {
  const asset: MediaAsset = { id: "asset-1", usage_type: "scene_background", media_type: "image/png", width: 1920, height: 1080, sha256: "a".repeat(64) };
  const ready: MediaAssetVariant = { id: "variant-1", asset_id: asset.id, status: "ready", width: 1920, height: 1080, opaque: true };
  assert.equal(requireReadyMediaVariant(asset, ready), ready);
  assert.throws(() => requireReadyMediaVariant(asset, { ...ready, status: "queued" }), /media_variant_not_ready/);
  assert.throws(() => requireReadyMediaVariant(asset, { ...ready, status: "failed", last_error_code: "unsupported_image" }), /unsupported_image/);
  assert.throws(() => requireReadyMediaVariant(asset, { ...ready, status: "failed", last_error_code: "raw backend detail" }), /media_variant_failed/);
  assert.throws(() => requireReadyMediaVariant(asset, { ...ready, asset_id: "asset-other" }), /media_variant_integrity/);
  assert.throws(() => requireReadyMediaVariant(asset, { ...ready, width: 1919 }), /media_variant_integrity/);
  assert.throws(() => requireReadyMediaVariant(asset, { ...ready, opaque: false }), /media_variant_integrity/);
});

test("Discord preset form and stream selection emit only the active mode", () => {
  const presetInput = { name: "  Main stage  ", guildID: " 123 ", textChannelID: "456", voiceChannelID: "789", revision: 4 };
  assert.equal(validDiscordTargetPreset(presetInput), true);
  assert.deepEqual(buildDiscordTargetPresetPayload(presetInput), { name: "Main stage", guild_id: "123", text_channel_id: "456", voice_channel_id: "789", expected_revision: 4 });
  for (const invalid of ["", "-1", "1.2", "a12", "1".repeat(33)]) assert.equal(validDiscordOpaqueID(invalid), false);

  const inherit = buildDiscordTargetPayload({ mode: "inherit", guildID: "ignored", presetID: "ignored" });
  assert.deepEqual(inherit, { discord_target_mode: "inherit" });
  const preset = buildDiscordTargetPayload({ mode: "preset", presetID: "preset-1", presetRevision: 8, guildID: "ignored" });
  assert.deepEqual(preset, { discord_target_mode: "preset", discord_target_preset_id: "preset-1", discord_target_preset_revision: 8 });
  const manual = buildDiscordTargetPayload({ mode: "manual", guildID: "11", textChannelID: "22", voiceChannelID: "33", presetID: "ignored", presetRevision: 9 });
  assert.deepEqual(manual, { discord_target_mode: "manual", discord_guild_id: "11", discord_text_channel_id: "22", discord_voice_channel_id: "33" });
});

test("visual update preserves omit versus explicit clear and presentation does not rewrite stream name", () => {
  const omitted = buildVisualUpdate(3, { header_title_mode: "custom", header_title_value: "Program title" });
  assert.equal("background_asset_id" in omitted, false);
  const cleared = buildVisualUpdate(3, { background_mode: null, background_asset_id: null, background_variant_id: null });
  assert.equal(cleared.background_asset_id, null);
  assert.equal(cleared.background_variant_id, null);

  const presentation = visualPresentation({
    stream_id: "stream-1", background_mode: "image", header_title_mode: "custom", header_title_value: "Presentation only",
    discord_target_mode: "preset", discord_target_preset_revision: 4, discord_snapshot_revision: 6, discord_preset_deleted: true,
    cover_source: "upload", cover_start_active: true, revision: 3,
  }, "Stored stream name");
  assert.equal(presentation.title, "Presentation only");
  assert.match(presentation.background, /center crop/);
  assert.match(presentation.warning, /snapshot/);
  assert.notEqual(presentation.title, "Stored stream name", "custom header is presentation-only and must not be used as a stream-name mutation");
});

test("cover controller blocks unknown, denied, unconfirmed, and duplicate pending actions before the handler", async () => {
  let snapshot: CoverPermissionSnapshot = { status: "loading", permissions: [] };
  let requests = 0;
  let release!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  const state: VideoCoverState = {
    stream_id: "stream-1", job_generation: 4, desired_active: false, desired_revision: 7,
    applied_active: null, applied_revision: null, status: "idle",
    pipeline_order: ["base_or_worker_scene", "video_cover", "watermark", "video_encode", "tee_live_archive_preview"],
    cover_watermark_independent: true,
  };
  const controller = createVideoCoverActionController(() => snapshot, async (request) => {
    requests++;
    await gate;
    return request;
  });
  assert.equal((await controller.issue(true, state, "show-1")).sent, false);
  snapshot = { status: "ready", permissions: [] };
  assert.equal((await controller.issue(true, state, "show-1")).sent, false);
  snapshot = { status: "ready", permissions: ["streams.show_cover", "streams.hide_cover"] };
  assert.equal((await controller.issue(false, state, "hide-1", false)).sent, false);
  assert.equal(requests, 0);

  const first = controller.issue(true, state, "show-1");
  await new Promise((resolve) => setTimeout(resolve, 0));
  const duplicate = await controller.issue(true, state, "show-1");
  assert.equal(duplicate.sent, false);
  assert.equal(requests, 1);
  release();
  assert.equal((await first).sent, true);
  assert.deepEqual(buildVideoCoverAction(false, state, "hide-2", true), { active: false, expected_job_generation: 4, expected_revision: 7, idempotency_key: "hide-2", hide_confirmed: true });
  assert.deepEqual(state.pipeline_order, ["base_or_worker_scene", "video_cover", "watermark", "video_encode", "tee_live_archive_preview"]);
  assert.equal(state.cover_watermark_independent, true);
});

test("cover idempotency keys retain cryptographic UUID v4 semantics without randomUUID", () => {
  const random = {
    randomUUID: undefined,
    getRandomValues<T extends ArrayBufferView | null>(array: T): T {
      const bytes = array as Uint8Array;
      bytes.forEach((_, index) => { bytes[index] = index; });
      return array;
    },
  } as unknown as Pick<Crypto, "getRandomValues" | "randomUUID">;
  assert.equal(newVideoCoverIdempotencyKey(random), "00010203-0405-4607-8809-0a0b0c0d0e0f");
  assert.throws(
    () => newVideoCoverIdempotencyKey({} as Pick<Crypto, "getRandomValues" | "randomUUID">),
    /video_cover_secure_random_unavailable/,
  );
});

test("ambiguous cover state blocks resend until a read-only reconciliation refresh", async () => {
	const requests: ReturnType<typeof buildVideoCoverAction>[] = [];
	const controller = createVideoCoverActionController(
		() => ({ status: "ready", permissions: ["streams.hide_cover"] }),
		async (request) => { requests.push({ ...request }); return request; },
	);
	const originalState: VideoCoverState = { stream_id: "stream-1", job_generation: 9, desired_active: true, desired_revision: 12, applied_active: true, applied_revision: 12, status: "applied", pipeline_order: [], cover_watermark_independent: true };
	const original = buildVideoCoverAction(false, originalState, "hide-reconcile", true);
	assert.equal((await controller.issueRequest(original)).sent, true);
	controller.holdForReconciliation();
	assert.equal((await controller.issueRequest(original)).sent, false);
	assert.deepEqual(requests, [original]);
	controller.reconcile();
	assert.equal(controller.unresolved, false);
});

test("backend cover rejection is one request with no automatic resend", async () => {
  let requests = 0;
  const controller = createVideoCoverActionController(
    () => ({ status: "ready", permissions: ["streams.show_cover"] }),
    async () => { requests++; throw new Error("backend rejected"); },
  );
  const state: VideoCoverState = { stream_id: "stream-1", job_generation: 1, desired_active: false, desired_revision: 1, applied_active: null, applied_revision: null, status: "idle", pipeline_order: [], cover_watermark_independent: true };
  await assert.rejects(controller.issue(true, state, "show-failed"), /backend rejected/);
  await new Promise((resolve) => setTimeout(resolve, 10));
  assert.equal(requests, 1);
});
