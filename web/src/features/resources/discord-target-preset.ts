export type DiscordTargetPresetInput = {
  name: string;
  guildID: string;
  textChannelID: string;
  voiceChannelID: string;
  revision?: number;
};

export function validDiscordOpaqueID(value: string) {
  return /^[0-9]{1,32}$/.test(value.trim());
}

export function validDiscordTargetPreset(input: DiscordTargetPresetInput) {
  return input.name.trim().length > 0
    && input.name.trim().length <= 128
    && validDiscordOpaqueID(input.guildID)
    && validDiscordOpaqueID(input.textChannelID)
    && validDiscordOpaqueID(input.voiceChannelID);
}

export function buildDiscordTargetPresetPayload(input: DiscordTargetPresetInput) {
  return {
    name: input.name.trim(),
    guild_id: input.guildID.trim(),
    text_channel_id: input.textChannelID.trim(),
    voice_channel_id: input.voiceChannelID.trim(),
    ...(input.revision === undefined ? {} : { expected_revision: input.revision }),
  };
}
