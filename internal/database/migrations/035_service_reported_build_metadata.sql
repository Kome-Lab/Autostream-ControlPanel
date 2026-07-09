ALTER TABLE services ADD COLUMN IF NOT EXISTS reported_commit VARCHAR(80) NOT NULL DEFAULT '' AFTER reported_version;
ALTER TABLE services ADD COLUMN IF NOT EXISTS reported_build_date VARCHAR(80) NOT NULL DEFAULT '' AFTER reported_commit;
