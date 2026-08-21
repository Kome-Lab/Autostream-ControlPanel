CREATE INDEX IF NOT EXISTS idx_stream_logs_created_stream
  ON stream_logs (created_at, stream_id, id);

CREATE INDEX IF NOT EXISTS idx_stream_logs_stream_created
  ON stream_logs (stream_id, created_at, id);

INSERT IGNORE INTO stream_logs (id, stream_id, level, message, fields, created_at)
SELECT
  audit_logs.id,
  audit_logs.resource_id,
  CASE WHEN audit_logs.result = 'failure' THEN 'error' ELSE 'info' END,
  audit_logs.action,
  JSON_OBJECT(
    'source', 'audit_backfill',
    'action', audit_logs.action,
    'result', audit_logs.result,
    'actor_username', COALESCE(audit_logs.actor_username, ''),
    'request_id', audit_logs.request_id
  ),
  audit_logs.timestamp
FROM audit_logs
INNER JOIN streams ON streams.id = audit_logs.resource_id
WHERE audit_logs.resource_type = 'stream'
  AND audit_logs.resource_id IS NOT NULL
  AND audit_logs.resource_id <> ''
  AND NOT (audit_logs.action = 'streams.retry_upload' AND audit_logs.result = 'success');

DROP TRIGGER IF EXISTS prevent_stream_log_delete;

CREATE TRIGGER prevent_stream_log_delete
BEFORE DELETE ON stream_logs
FOR EACH ROW
SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'stream log history is append-only';
