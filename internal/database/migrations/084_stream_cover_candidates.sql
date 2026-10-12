CREATE TABLE stream_video_cover_candidates (
  stream_id CHAR(36) NOT NULL,
  variant_id CHAR(36) NOT NULL,
  asset_id CHAR(36) NOT NULL,
  display_name VARCHAR(128) NOT NULL,
  source ENUM('preset','upload') NOT NULL,
  preset_id CHAR(36) NULL,
  preset_revision BIGINT UNSIGNED NULL,
  sort_order INT UNSIGNED NOT NULL,
  PRIMARY KEY(stream_id,variant_id),
  CONSTRAINT fk_cover_candidate_stream FOREIGN KEY(stream_id) REFERENCES streams(id) ON DELETE CASCADE,
  CONSTRAINT fk_cover_candidate_variant FOREIGN KEY(variant_id) REFERENCES media_asset_variants(id) ON DELETE RESTRICT,
  CONSTRAINT fk_cover_candidate_asset FOREIGN KEY(asset_id) REFERENCES media_assets(id) ON DELETE RESTRICT
);
INSERT INTO stream_video_cover_candidates(stream_id,variant_id,asset_id,display_name,source,preset_id,preset_revision,sort_order)
SELECT stream_id,cover_variant_id,cover_asset_id,'フタ画像',cover_source,cover_preset_id,cover_preset_revision,0
FROM stream_visual_settings WHERE cover_source IN ('preset','upload') AND cover_variant_id IS NOT NULL AND cover_asset_id IS NOT NULL;
ALTER TABLE stream_video_cover_runtime ADD COLUMN applied_asset_variant_id CHAR(36) NULL;
UPDATE stream_video_cover_runtime SET applied_asset_variant_id=asset_variant_id WHERE applied_active=TRUE AND applied_revision IS NOT NULL;
ALTER TABLE stream_video_cover_actions ADD COLUMN requested_asset_variant_id CHAR(36) NULL;
UPDATE stream_video_cover_actions a JOIN stream_video_cover_runtime r ON r.stream_id=a.stream_id AND r.job_generation=a.job_generation
SET a.requested_asset_variant_id=r.asset_variant_id;
CREATE TABLE stream_normal_stop_receipts (
 stream_id CHAR(36) NOT NULL,
 source_updated_at VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 completed_updated_at DATETIME(6) NOT NULL,
 PRIMARY KEY(stream_id,source_updated_at),
 CONSTRAINT fk_normal_stop_receipt_stream FOREIGN KEY(stream_id) REFERENCES streams(id) ON DELETE CASCADE
);

ALTER TABLE stream_video_cover_runtime ADD COLUMN retired_at DATETIME(6) NULL;
CREATE TABLE stream_normal_stop_pending (
 stream_id CHAR(36) NOT NULL PRIMARY KEY,
 CONSTRAINT fk_normal_stop_pending_stream FOREIGN KEY(stream_id) REFERENCES streams(id) ON DELETE CASCADE
);
