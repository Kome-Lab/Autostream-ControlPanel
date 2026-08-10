-- Migration 066 may already have run in a WIP or CI database. Keep these
-- additive fields separate so both existing and fresh claim tables gain the
-- provider Prepare fence and durable Encoder Stop receipt safely.
ALTER TABLE stream_youtube_relay_binding_claims
  ADD COLUMN IF NOT EXISTS prepare_state ENUM('not_attempted','possibly_prepared') NOT NULL DEFAULT 'not_attempted' AFTER state;

ALTER TABLE stream_youtube_relay_binding_claims
  ADD COLUMN IF NOT EXISTS encoder_stop_confirmed_at DATETIME(6) NULL AFTER dispatch_state;
