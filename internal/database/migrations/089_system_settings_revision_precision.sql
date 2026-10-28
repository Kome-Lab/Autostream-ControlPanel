-- Preserve saved APP/Security timestamps while giving their independent
-- configuration rows a microsecond compare-and-swap authority.
ALTER TABLE system_settings MODIFY COLUMN updated_at DATETIME(6) NOT NULL;
