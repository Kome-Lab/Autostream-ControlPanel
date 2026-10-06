import "./component-loader.mts";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createElement } from "react";
import test from "node:test";
import type { CurrentUser } from "../../src/types/domain.ts";
import { renderUI } from "./render-ui.mts";
import { actualCallback, actualJSXCallback } from "./source-callback.mts";

const { resourcePages } = await import("../../src/features/resources/resource-config.ts");
const { ResourcePage, preferredResourceTab } = await import("../../src/features/resources/resource-page.tsx");
const { ResourceFormFields } = await import("../../src/features/resources/resource-form-fields.tsx");
const { visibleNavigationSections } = await import("../../src/lib/navigation.ts");
const { PresetListStatus, visualPresetOptions, buildStreamVisualSaveFields } = await import("../../src/features/streams/stream-visual-settings-section.tsx");
const { defaultStreamVisualDraft } = await import("../../src/features/streams/stream-visual-draft.ts");
const visualSource = readFileSync(new URL("../../src/features/streams/stream-visual-settings-section.tsx", import.meta.url), "utf8");
const coverFormSource = readFileSync(new URL("../../src/features/resources/resource-video-cover-form.tsx", import.meta.url), "utf8");
const currentUser = (permissions: string[]): CurrentUser => ({ user: { id: "operator", username: "Operator", roles: [] }, permissions });

test("new stream visual controls use create permission independently of update permission", async () => {
  const { StreamSlotForm } = await import("../../src/features/streams/stream-slot-form.tsx");
  const { createStreamActionController } = await import("../../src/features/streams/stream-action-controller.ts");
  const actionController = createStreamActionController({
    getPermissions: () => ({ kind: "ready", permissions: [] }),
    getState: () => ({ kind: "ready", freshness: "fresh", fingerprint: "synthetic" }),
    mutate: async () => { assert.fail("rendering must not submit a stream mutation"); },
  });
  for (const [canCreate, canUpdate] of [[true, false], [false, true], [false, false]] as const) {
    const html = renderUI(createElement(StreamSlotForm, {
      actionController, canCreate, canUpdate, canAssignEncoder: false, canAssignWorker: false,
      onActionResult() {}, onSaved() {},
    }), "en", "/admin/streams/", client => client.setQueryData(["auth", "me"], currentUser([
      ...(canCreate ? ["streams.create"] : []), ...(canUpdate ? ["streams.update"] : []),
    ])));
    const start = html.indexOf('id="create-stream-visual"');
    const end = html.indexOf('id="create-stream-encoder"', start);
    assert.ok(start >= 0 && end > start, "real create form must render its visual section");
    const visual = html.slice(start, end);
    const modes = [...visual.matchAll(/<button\b[^>]*role="combobox"[^>]*>/g)].map(match => match[0]);
    assert.equal(modes.length, 4, "background, title, Discord target and cover modes are rendered");
    for (const mode of modes) {
      assert.equal(/\sdisabled(?:=|\s|>)/.test(mode), !canCreate,
        `create=${canCreate}, update=${canUpdate}: visual modes follow the create authority`);
    }
  }
});

test("preset-only read permission reaches the correct navigation page and initial tab", () => {
  const cases = [
    { page: "discord" as const, permission: "discord_target_presets.read", path: "/discord/target-presets", href: "/admin/discord/" },
    { page: "overlay" as const, permission: "video_cover_presets.read", path: "/video-cover-presets", href: "/admin/overlay/" },
  ];
  for (const value of cases) {
    const user = currentUser([value.permission]);
    assert.ok(visibleNavigationSections(user).flatMap((section) => section.items).some((item) => item.href === value.href));
    assert.equal(preferredResourceTab(resourcePages[value.page].resources, user), value.path);
    assert.equal(preferredResourceTab(resourcePages[value.page].resources, user, "not-a-resource"), value.path);
    const html = renderUI(createElement(ResourcePage, { pageId: value.page }), "en", value.href, (client) => {
      client.setQueryData(["auth", "me"], user);
      client.setQueryData(["resource", value.path], { items: [] });
    });
    const selectedTab = html.match(/<button[^>]*aria-selected="true"[^>]*>[\s\S]*?<\/button>/)?.[0];
    assert.ok(selectedTab);
    assert.match(selectedTab, value.page === "discord" ? /Discord target presets/ : /Video cover presets/);
    assert.doesNotMatch(html, /You do not have permission to View this item/);
  }
  assert.equal(preferredResourceTab(resourcePages.overlay.resources, currentUser(["*"]), "video-cover-presets"), "/video-cover-presets");
  assert.equal(preferredResourceTab(resourcePages.overlay.resources, currentUser(["overlay_profiles.read"]), "video-cover-presets"), "/profiles/overlay");
});

test("the cover resource renders its real create and edit form through the production dispatch", () => {
  const cover = resourcePages.overlay.resources.find((resource) => resource.path === "/video-cover-presets");
  assert.ok(cover);
  const submit = () => { assert.fail("server rendering must not submit a mutation"); };
  const permissions = ["video_cover_presets.read", "video_cover_presets.create", "video_cover_presets.update"];
  const configure = (client: import("@tanstack/react-query").QueryClient) => client.setQueryData(["auth", "me"], currentUser(permissions));
  const create = renderUI(createElement(ResourceFormFields, { resource: cover, disabled: false, submit }), "en", "/admin/overlay/", configure);
  assert.match(create, /type="file"/);
  assert.match(create, /accept="image\/png,image\/jpeg,image\/webp"/);
  assert.match(create, /No image is selected/);
  assert.match(create, /Image upload requires stream create or update permission/);
  assert.match(create, /<button[^>]*type="submit"[^>]*disabled/);

  const edit = renderUI(createElement(ResourceFormFields, { resource: cover, disabled: false, submit, initial: { id: "cover-1", name: "Stand by", enabled: true, revision: 4, asset_id: "saved-asset", asset_variant_id: "saved-variant" }, submitLabel: "Update" }), "en", "/admin/overlay/", configure);
  assert.match(edit, /The saved image is selected/);
  const submitButton = edit.match(/<button[^>]*type="submit"[^>]*>/)?.[0];
  assert.ok(submitButton);
  assert.doesNotMatch(submitButton, /\sdisabled(?:=|\s|>)/);
  assert.doesNotMatch(edit, /value="saved-asset"|value="saved-variant"/, "immutable asset IDs are not editable text fields");
});

test("cover form submits verified asset references with the original edit revision", () => {
  const requests: unknown[] = [];
  const submit = actualJSXCallback(coverFormSource, "form", "onSubmit", {
    disabled: false, uploading: false, ready: true, name: "  Stand by  ", enabled: true,
    image: { assetID: "asset-ready", variantID: "variant-ready" },
    initial: { id: "cover-1", revision: 9 }, baseline: { revision: 4 },
    submit: (payload: unknown) => requests.push(payload),
  });
  submit({ preventDefault() {} });
  assert.deepEqual(requests, [{ name: "Stand by", asset_id: "asset-ready", asset_variant_id: "variant-ready", enabled: true, expected_revision: 4 }]);
});

test("preset fetch failure is distinct from an empty list and management preserves the current tab", () => {
  for (const locale of ["ja", "en"] as const) {
    const common = { locale, allowed: true, authorityStatus: "success" as const, fetching: false, count: 0, onRefresh() {}, manageHref: "/admin/overlay/#video-cover-presets", manageLabel: locale === "ja" ? "蓋画像プリセット管理（別タブ）" : "Manage video cover presets (new tab)" };
    const failed = renderUI(createElement(PresetListStatus, { ...common, status: "error" }), locale);
    const empty = renderUI(createElement(PresetListStatus, { ...common, status: "success" }), locale);
    assert.match(failed, /role="alert"/);
    assert.match(failed, locale === "ja" ? /現在の選択と入力内容は保持/ : /Your selection and input are unchanged/);
    assert.doesNotMatch(failed, locale === "ja" ? /まだありません/ : /No presets are available/);
    assert.match(empty, locale === "ja" ? /まだありません/ : /No presets are available/);
    assert.match(failed, /href="\/admin\/overlay\/#video-cover-presets" target="_blank" rel="noopener noreferrer"/);
    const denied = renderUI(createElement(PresetListStatus, { ...common, allowed: false, status: "pending" }), locale);
    assert.match(denied, locale === "ja" ? /参照する権限がありません/ : /do not have preset read permission/);
    assert.doesNotMatch(denied, /<a\b/, "denied users are not given a misleading management link");
  }
});

test("preset option refresh keeps missing references and displays the selected snapshot revision", () => {
  const rows = [{ id: "preset-1", name: "Main stage", revision: 7 }];
  assert.deepEqual(visualPresetOptions(rows, "preset-1", "Saved selection", 4), [["preset-1", "Main stage (r4)"]]);
  assert.deepEqual(visualPresetOptions([], "preset-1", "Saved selection", 4), [["preset-1", "Saved selection", true]]);
  assert.deepEqual(rows, [{ id: "preset-1", name: "Main stage", revision: 7 }]);
});

test("changing only cover start visibility retains the saved image even without preset access", () => {
  const draft = { ...defaultStreamVisualDraft(), coverSource: "preset" as const, coverPresetID: "retired-preset", coverAssetID: "saved-asset", coverVariantID: "saved-variant", coverStartActive: true };
  assert.deepEqual(buildStreamVisualSaveFields(draft, new Set(["cover"]), true), { cover_start_active: true });
  assert.deepEqual(buildStreamVisualSaveFields(draft, new Set(["cover"]), false), { cover_source: "preset", cover_preset_id: "retired-preset", cover_start_active: true });
  const required = actualCallback(visualSource, "coverPresetRequired", { editing: true, coverSelectionDirty: false, dirtySections: new Set(["cover"]), draft });
  assert.equal(required, false, "an unchanged preset must not require a new list read or selection");
  const selections: boolean[] = [];
  const update = actualCallback(visualSource, "update", { editing: true, setCoverSelectionDirty: (value: boolean) => selections.push(value), setDirtySections() {}, setDraft() {} });
  update("cover", { coverStartActive: false });
  assert.deepEqual(selections, []);
  update("cover", { coverPresetID: "new-preset" });
  assert.deepEqual(selections, [true]);
});

test("failed visual reload with cached data does not discard an edited draft or reconcile a write", async () => {
  let writes = 0;
  const messages: string[] = [];
  const refresh = actualCallback(visualSource, "refresh", {
    visual: { refetch: async () => ({ isSuccess: false, data: { revision: 3 } }) },
    controller: { reconcile: () => { writes++; } },
    setNeedsRefresh: () => { writes++; }, setDraft: () => { writes++; }, setDraftRevision: () => { writes++; }, setDirtySections: () => { writes++; },
    setCoverPresetRevision: () => { writes++; }, setCoverSelectionDirty: () => { writes++; }, setUploadedBackground: () => { writes++; }, setUploadedCover: () => { writes++; },
    previewOwner: { release: () => { writes++; } }, setBackgroundPreview: () => { writes++; }, setCoverPreview: () => { writes++; },
    setMessage: (value: string) => messages.push(value), locale: "en",
  });
  await refresh();
  assert.equal(writes, 0);
  assert.deepEqual(messages, ["The settings could not be reloaded. Your draft is unchanged."]);
});
