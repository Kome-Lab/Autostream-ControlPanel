ALTER TABLE stream_service_assignments
  ADD COLUMN assignment_role ENUM('primary','standby') NOT NULL DEFAULT 'primary' AFTER service_type;

ALTER TABLE stream_service_assignments
  DROP INDEX uniq_stream_service_type;

ALTER TABLE stream_service_assignments
  ADD UNIQUE KEY uniq_stream_service (stream_id, service_id),
  ADD INDEX idx_stream_service_role (stream_id, service_type, assignment_role);
