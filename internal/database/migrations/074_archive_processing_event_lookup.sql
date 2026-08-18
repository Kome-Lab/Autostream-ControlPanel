CREATE INDEX IF NOT EXISTS idx_service_stream_events_stream_type
  ON service_stream_events (stream_id, event_type);
