-- IU-WS2-10: explicit, isolated application only. Ordinary startup excludes this file.
-- Historical Streams have no observed unheld default and are not backfilled.
CREATE TABLE stream_auto_start_holds (
  stream_id CHAR(36) NOT NULL PRIMARY KEY,
  revision BIGINT UNSIGNED NOT NULL,
  body LONGTEXT NOT NULL,
  CONSTRAINT fk_manual_hold_stream FOREIGN KEY (stream_id) REFERENCES streams(id) ON DELETE RESTRICT
);

CREATE TABLE stream_auto_start_hold_history (
  sequence BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  stream_id CHAR(36) NOT NULL,
  request_id VARCHAR(96) NOT NULL,
  hold_revision BIGINT UNSIGNED NOT NULL,
  body LONGTEXT NOT NULL,
  KEY ix_manual_hold_history_stream (stream_id, sequence),
  CONSTRAINT fk_manual_hold_history_stream FOREIGN KEY (stream_id) REFERENCES streams(id) ON DELETE RESTRICT
);

CREATE TABLE stream_auto_start_attempts (
  attempt_id VARCHAR(96) NOT NULL PRIMARY KEY,
  stream_id CHAR(36) NOT NULL,
  request_body LONGTEXT NOT NULL,
  body LONGTEXT NOT NULL,
  KEY ix_manual_hold_attempt_stream (stream_id, attempt_id),
  CONSTRAINT fk_manual_hold_attempt_stream FOREIGN KEY (stream_id) REFERENCES streams(id) ON DELETE RESTRICT
);

CREATE TABLE stream_auto_start_admissions (
  request_id VARCHAR(96) NOT NULL PRIMARY KEY,
  stream_id CHAR(36) NOT NULL,
  body LONGTEXT NOT NULL,
  KEY ix_manual_hold_admission_stream (stream_id, request_id),
  CONSTRAINT fk_manual_hold_admission_stream FOREIGN KEY (stream_id) REFERENCES streams(id) ON DELETE RESTRICT
);

CREATE TABLE stream_auto_start_claim_requests (
  request_id VARCHAR(96) NOT NULL PRIMARY KEY,
  stream_id CHAR(36) NOT NULL,
  body LONGTEXT NOT NULL,
  KEY ix_manual_start_claim_stream (stream_id, request_id),
  CONSTRAINT fk_manual_start_claim_stream FOREIGN KEY (stream_id) REFERENCES streams(id) ON DELETE RESTRICT
);
