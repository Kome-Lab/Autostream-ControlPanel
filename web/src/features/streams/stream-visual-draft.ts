import {
  buildDiscordTargetPayload,
  type DiscordTargetMode,
  type StreamVisualSettings,
} from "@/features/streams/control-platform";

export type StreamVisualDraft = {
  backgroundMode: "default" | "image";
  backgroundAssetID: string;
  backgroundVariantID: string;
  headerTitleMode: "default" | "custom";
  headerTitleValue: string;
  discordTargetMode: DiscordTargetMode;
  discordTargetPresetID: string;
  discordTargetPresetRevision: number;
  discordGuildID: string;
  discordTextChannelID: string;
  discordVoiceChannelID: string;
  coverSource: "none" | "preset" | "upload";
  coverPresetID: string;
  coverAssetID: string;
  coverVariantID: string;
  coverStartActive: boolean;
  uploadSessionID: string;
};

export type StreamVisualValidation = Readonly<{
  ready: boolean;
  issues: readonly string[];
}>;

export type StreamVisualSection = "background" | "title" | "discord" | "cover";
export type StreamVisualPreviewKind = "background" | "cover";

export function createStreamVisualPreviewOwner(revoke: (url: string) => void) {
  const current: Record<StreamVisualPreviewKind, string> = { background: "", cover: "" };
  return Object.freeze({
    replace(kind: StreamVisualPreviewKind, url: string) {
      const previous = current[kind];
      if (previous && previous !== url) revoke(previous);
      current[kind] = url;
      return url;
    },
    release() {
      const urls = new Set(Object.values(current).filter(Boolean));
      current.background = "";
      current.cover = "";
      for (const url of urls) revoke(url);
    },
  });
}

export function defaultStreamVisualDraft(): StreamVisualDraft {
  return {
    backgroundMode: "default",
    backgroundAssetID: "",
    backgroundVariantID: "",
    headerTitleMode: "default",
    headerTitleValue: "",
    discordTargetMode: "inherit",
    discordTargetPresetID: "",
    discordTargetPresetRevision: 0,
    discordGuildID: "",
    discordTextChannelID: "",
    discordVoiceChannelID: "",
    coverSource: "none",
    coverPresetID: "",
    coverAssetID: "",
    coverVariantID: "",
    coverStartActive: false,
    uploadSessionID: "",
  };
}

export function streamVisualDraftFromSettings(settings: StreamVisualSettings): StreamVisualDraft {
  return {
    backgroundMode: settings.background_mode,
    backgroundAssetID: settings.background_asset_id || "",
    backgroundVariantID: settings.background_variant_id || "",
    headerTitleMode: settings.header_title_mode,
    headerTitleValue: settings.header_title_value || "",
    discordTargetMode: settings.discord_target_mode || "inherit",
    discordTargetPresetID: settings.discord_target_preset_id || "",
    discordTargetPresetRevision: settings.discord_target_preset_revision || 0,
    discordGuildID: settings.discord_guild_id || "",
    discordTextChannelID: settings.discord_text_channel_id || "",
    discordVoiceChannelID: settings.discord_voice_channel_id || "",
    coverSource: settings.cover_source,
    coverPresetID: settings.cover_preset_id || "",
    coverAssetID: settings.cover_asset_id || "",
    coverVariantID: settings.cover_variant_id || "",
    coverStartActive: settings.cover_source === "none" ? false : settings.cover_start_active,
    uploadSessionID: "",
  };
}

export function buildStreamVisualFields(
  draft: StreamVisualDraft,
  sections: ReadonlySet<StreamVisualSection> = new Set(["background", "title", "discord", "cover"]),
): Record<string, unknown> {
  const background = draft.backgroundMode === "image"
    ? {
        background_mode: "image",
        background_asset_id: draft.backgroundAssetID.trim(),
        background_variant_id: draft.backgroundVariantID.trim(),
      }
    : { background_mode: "default", background_asset_id: null, background_variant_id: null };
  const title = draft.headerTitleMode === "custom"
    ? { header_title_mode: "custom", header_title_value: draft.headerTitleValue.trim() }
    : { header_title_mode: "default", header_title_value: null };
  const discord = buildDiscordTargetPayload({
    mode: draft.discordTargetMode,
    presetID: draft.discordTargetPresetID,
    presetRevision: draft.discordTargetPresetRevision,
    guildID: draft.discordGuildID,
    textChannelID: draft.discordTextChannelID,
    voiceChannelID: draft.discordVoiceChannelID,
  });
  const cover = draft.coverSource === "none"
    ? {
        cover_source: "none",
        cover_preset_id: null,
        cover_asset_id: null,
        cover_variant_id: null,
        cover_start_active: false,
      }
    : draft.coverSource === "preset"
      ? {
          cover_source: "preset",
          cover_preset_id: draft.coverPresetID.trim(),
          cover_start_active: draft.coverStartActive,
        }
      : {
          cover_source: "upload",
          cover_asset_id: draft.coverAssetID.trim(),
          cover_variant_id: draft.coverVariantID.trim(),
          cover_start_active: draft.coverStartActive,
        };
  return {
    ...(sections.has("background") ? background : {}),
    ...(sections.has("title") ? title : {}),
    ...(sections.has("discord") ? discord : {}),
    ...(sections.has("cover") ? cover : {}),
  };
}

export function buildStreamCreateVisualExtension(draft: StreamVisualDraft, _sections: ReadonlySet<StreamVisualSection>) {
  return {
    ...(draft.uploadSessionID ? { upload_session_id: draft.uploadSessionID } : {}),
    visual_settings: buildStreamVisualFields(draft),
  };
}

export function validateStreamVisualDraft(draft: StreamVisualDraft): StreamVisualValidation {
  const issues: string[] = [];
  if (draft.backgroundMode === "image" && (!draft.backgroundAssetID.trim() || !draft.backgroundVariantID.trim())) {
    issues.push("背景画像の処理完了を待ってください。");
  }
  if (draft.headerTitleMode === "custom" && !validHeaderTitle(draft.headerTitleValue)) {
    issues.push("カスタムタイトルは制御文字を含まない1〜80文字で指定してください。");
  }
  if (draft.discordTargetMode === "preset" && (!draft.discordTargetPresetID.trim() || draft.discordTargetPresetRevision < 1)) {
    issues.push("Discord配信先プリセットを選択してください。");
  }
  if (draft.discordTargetMode === "manual" && ![
    draft.discordGuildID,
    draft.discordTextChannelID,
    draft.discordVoiceChannelID,
  ].every(validDiscordID)) {
    issues.push("DiscordのGuild、Text、Voice IDをすべて指定してください。");
  }
  if (draft.coverSource === "preset" && !draft.coverPresetID.trim()) {
    issues.push("Video Coverプリセットを選択してください。");
  }
  if (draft.coverSource === "upload" && (!draft.coverAssetID.trim() || !draft.coverVariantID.trim())) {
    issues.push("Video Cover画像の処理完了を待ってください。");
  }
  return Object.freeze({ ready: issues.length === 0, issues: Object.freeze(issues) });
}

export function visualCapabilityWarnings(
  draft: StreamVisualDraft,
  capabilities: Readonly<{ sceneAppearance: boolean; videoCover: boolean }> | undefined,
) {
  if (!capabilities) return [] as string[];
  const warnings: string[] = [];
  if ((draft.backgroundMode === "image" || draft.headerTitleMode === "custom") && !capabilities.sceneAppearance) {
    warnings.push("割当済みWorkerが scene_appearance_v1 を報告するまで開始ReadinessはBLOCKされ、scene payloadは送信されません。");
  }
  if (draft.coverSource !== "none" && !capabilities.videoCover) {
    warnings.push("割当済みEncoderが live_video_cover_v1 を報告するまで開始ReadinessとCover操作はBLOCKされ、Cover runtime payloadは送信されません。");
  }
  return warnings;
}

function validDiscordID(value: string) {
  return /^\d{1,32}$/.test(value.trim());
}

function validHeaderTitle(value: string) {
  const title = value.trim();
  if (!title || [...title].length > 80) return false;
  return !/[\u0000-\u001f\u007f\u061c\u200e\u200f\u202a-\u202e\u2066-\u2069]/u.test(title);
}
