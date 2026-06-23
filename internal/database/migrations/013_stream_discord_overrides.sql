ALTER TABLE stream_settings
  ADD COLUMN IF NOT EXISTS discord_guild_id VARCHAR(255) NULL AFTER discord_config_id,
  ADD COLUMN IF NOT EXISTS discord_voice_channel_id VARCHAR(255) NULL AFTER discord_guild_id,
  ADD COLUMN IF NOT EXISTS discord_text_channel_id VARCHAR(255) NULL AFTER discord_voice_channel_id;
