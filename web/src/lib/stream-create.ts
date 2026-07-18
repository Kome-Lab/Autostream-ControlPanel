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
  encoderServiceID: string;
  workerServiceID: string;
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
    encoder_service_id: values.encoderServiceID,
    worker_service_id: values.workerServiceID,
  });
}

function compactRecord(record: Record<string, unknown>) {
  return Object.fromEntries(Object.entries(record).filter(([, value]) => value !== "" && value !== undefined));
}
