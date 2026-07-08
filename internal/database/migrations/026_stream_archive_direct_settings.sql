ALTER TABLE stream_settings ADD COLUMN IF NOT EXISTS archive_drive_destination_id CHAR(36) NULL AFTER archive_profile_id;
ALTER TABLE stream_settings ADD COLUMN IF NOT EXISTS archive_oauth_account_id CHAR(36) NULL AFTER archive_drive_destination_id;
ALTER TABLE stream_settings ADD COLUMN IF NOT EXISTS archive_shared_drive BOOLEAN NOT NULL DEFAULT FALSE AFTER archive_oauth_account_id;
ALTER TABLE stream_settings ADD COLUMN IF NOT EXISTS archive_shared_drive_id VARCHAR(255) NULL AFTER archive_shared_drive;
ALTER TABLE stream_settings ADD COLUMN IF NOT EXISTS archive_file_name VARCHAR(255) NULL AFTER archive_shared_drive_id;
