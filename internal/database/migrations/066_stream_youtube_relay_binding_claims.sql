-- A relay-static claim captures this revision under the output profile row
-- lock.  The composite foreign key below makes a concurrent youtube_output
-- profile update fail closed while the claim exists; deleting the profile is
-- already blocked by the direct output foreign key.
ALTER TABLE profiles
  ADD COLUMN IF NOT EXISTS youtube_relay_binding_revision BIGINT UNSIGNED NOT NULL DEFAULT 0;

ALTER TABLE profiles
  ADD UNIQUE KEY IF NOT EXISTS uq_profiles_youtube_relay_binding_revision (id, youtube_relay_binding_revision);

CREATE TABLE IF NOT EXISTS stream_youtube_relay_binding_claims (
  relay_binding_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  reservation_token CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  stream_id CHAR(36) NOT NULL,
  youtube_output_id CHAR(36) NOT NULL,
  youtube_output_revision BIGINT UNSIGNED NOT NULL,
  oauth_account_id CHAR(36) NOT NULL,
  reusable_live_stream_id VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  broadcast_id VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NULL,
  state ENUM('reserved','prepared','recovery_required') NOT NULL,
  -- This is deliberately not an acknowledgement.  It is a durable fence set
  -- immediately before the Panel invokes the downstream Start dispatcher.
  dispatch_state ENUM('not_dispatched','possibly_dispatched') NOT NULL DEFAULT 'not_dispatched',
  last_error VARCHAR(255) NOT NULL DEFAULT '',
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (relay_binding_id),
  UNIQUE KEY uq_yt_relay_claim_reservation_token (reservation_token),
  UNIQUE KEY uq_yt_relay_claim_stream (stream_id),
  UNIQUE KEY uq_yt_relay_claim_live_stream (oauth_account_id, reusable_live_stream_id),
  CONSTRAINT fk_yt_relay_claim_stream
    FOREIGN KEY (stream_id) REFERENCES streams(id) ON DELETE RESTRICT,
  CONSTRAINT fk_yt_relay_claim_youtube_output
    FOREIGN KEY (youtube_output_id) REFERENCES profiles(id) ON DELETE RESTRICT,
  CONSTRAINT fk_yt_relay_claim_youtube_output_revision
    FOREIGN KEY (youtube_output_id, youtube_output_revision)
      REFERENCES profiles(id, youtube_relay_binding_revision)
      ON UPDATE RESTRICT ON DELETE RESTRICT
);
