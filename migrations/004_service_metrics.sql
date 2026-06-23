ALTER TABLE services ADD COLUMN metrics JSON NULL AFTER capabilities;

UPDATE services SET metrics = JSON_OBJECT() WHERE metrics IS NULL;

ALTER TABLE services MODIFY COLUMN metrics JSON NOT NULL;
