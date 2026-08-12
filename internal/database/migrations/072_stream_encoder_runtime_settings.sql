ALTER TABLE stream_settings
  ADD COLUMN encoder_audio_gain_db DECIMAL(5,1) NOT NULL DEFAULT 0.0 AFTER overlay_profile_id;
