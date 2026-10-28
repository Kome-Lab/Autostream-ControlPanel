-- Preserve existing timestamps while giving profile and Drive edits a
-- microsecond CAS authority. Writers advance equal timestamps by one microsecond.
ALTER TABLE profiles MODIFY COLUMN updated_at DATETIME(6) NOT NULL;
ALTER TABLE drive_destinations MODIFY COLUMN updated_at DATETIME(6) NOT NULL;
