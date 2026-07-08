ALTER TABLE stream_settings ADD COLUMN IF NOT EXISTS auto_start_trigger VARCHAR(80) NOT NULL DEFAULT '' AFTER discord_text_channel_id;
