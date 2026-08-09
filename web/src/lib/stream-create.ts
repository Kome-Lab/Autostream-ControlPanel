export type StreamCreateValues = {
  name: string;
  discordConfigID: string;
  discordGuildID: string;
  discordVoiceChannelID: string;
  discordTextChannelID: string;
  autoStartFromDiscord: boolean;
  youtubeOutputID: string;
  archiveProfileID: string;
  encoderProfileID: string;
  captionProfileID: string;
  watermarkEnabled: boolean;
  overlayProfileID: string;
  // Undefined preserves a server-side assignment during an edit. An empty
  // string is an explicit "unassign" selected in the stream form.
  encoderServiceID?: string;
  workerServiceID?: string;
  scheduledStartAt?: string;
  scheduledEndAt?: string;
  encoderInputURL?: string;
};

export function buildStreamCreatePayload(values: StreamCreateValues): Record<string, unknown> {
  return compactRecord({
    name: values.name,
    discord_config_id: values.discordConfigID,
    discord_guild_id: values.discordGuildID,
    discord_voice_channel_id: values.discordVoiceChannelID,
    discord_text_channel_id: values.discordTextChannelID,
    auto_start_trigger: values.autoStartFromDiscord ? "discord_voice_join" : "",
    youtube_output_id: values.youtubeOutputID,
    archive_profile_id: values.archiveProfileID,
    encoder_profile_id: values.encoderProfileID,
    caption_profile_id: values.captionProfileID,
    overlay_profile_id: values.watermarkEnabled ? values.overlayProfileID : "",
    ...(values.encoderServiceID === undefined ? {} : { encoder_service_id: values.encoderServiceID }),
    ...(values.workerServiceID === undefined ? {} : { worker_service_id: values.workerServiceID }),
  });
}

// PUT /streams/:id/settings replaces the stored settings. Unlike the create
// request, edits must send intentional empty values so that a user can remove
// an optional profile without leaving an invisible old value behind.
export function buildStreamSettingsPayload(values: StreamCreateValues): Record<string, unknown> {
  return {
    name: values.name.trim(),
    discord_config_id: values.discordConfigID,
    discord_guild_id: values.discordGuildID.trim(),
    discord_voice_channel_id: values.discordVoiceChannelID.trim(),
    discord_text_channel_id: values.discordTextChannelID.trim(),
    auto_start_trigger: values.autoStartFromDiscord ? "discord_voice_join" : "",
    youtube_output_id: values.youtubeOutputID,
    archive_profile_id: values.archiveProfileID,
    encoder_profile_id: values.encoderProfileID,
    caption_profile_id: values.captionProfileID,
    overlay_profile_id: values.watermarkEnabled ? values.overlayProfileID : "",
    ...(values.encoderServiceID === undefined ? {} : { encoder_service_id: values.encoderServiceID }),
    ...(values.workerServiceID === undefined ? {} : { worker_service_id: values.workerServiceID }),
    scheduled_start_at: values.scheduledStartAt || "",
    scheduled_end_at: values.scheduledEndAt || "",
    encoder_input_url: values.encoderInputURL || "",
  };
}

function compactRecord(record: Record<string, unknown>) {
  return Object.fromEntries(Object.entries(record).filter(([, value]) => value !== "" && value !== undefined));
}
