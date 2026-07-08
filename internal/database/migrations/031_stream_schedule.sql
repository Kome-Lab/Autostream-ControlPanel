ALTER TABLE streams ADD COLUMN IF NOT EXISTS scheduled_start_at DATETIME NULL AFTER status;
ALTER TABLE streams ADD COLUMN IF NOT EXISTS scheduled_end_at DATETIME NULL AFTER scheduled_start_at;
