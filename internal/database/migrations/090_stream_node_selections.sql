-- Saved choices are independent from the single current execution owner.
-- Existing execution, stop, and archive owners are deliberately left intact.
CREATE TABLE IF NOT EXISTS stream_node_selections (
  stream_id CHAR(36) NOT NULL,
  service_type ENUM('discord_bot','encoder_recorder','worker') NOT NULL,
  service_id VARCHAR(128) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (stream_id, service_type),
  KEY idx_stream_node_selection_service (service_id, stream_id)
);

-- A duplicate primary role fails rather than arbitrarily choosing a Node.
-- NOT EXISTS makes a retry after an interrupted migration record harmless.
INSERT INTO stream_node_selections
  (stream_id, service_type, service_id, created_at, updated_at)
SELECT a.stream_id, a.service_type, a.service_id, a.assigned_at, a.assigned_at
FROM stream_service_assignments a
JOIN streams s ON s.id = a.stream_id
WHERE a.assignment_role = 'primary'
  AND a.service_type IN ('discord_bot','encoder_recorder','worker')
  AND s.deleted_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM stream_node_selections existing
    WHERE existing.stream_id = a.stream_id
      AND existing.service_type = a.service_type
  );
