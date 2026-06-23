DELETE stale_artifacts
FROM stream_artifacts AS stale_artifacts
JOIN stream_artifacts AS latest_artifacts
  ON stale_artifacts.stream_id = latest_artifacts.stream_id
  AND stale_artifacts.kind = latest_artifacts.kind
  AND stale_artifacts.name = latest_artifacts.name
  AND (
    stale_artifacts.created_at < latest_artifacts.created_at
    OR (
      stale_artifacts.created_at = latest_artifacts.created_at
      AND stale_artifacts.id < latest_artifacts.id
    )
  );

ALTER TABLE stream_artifacts
  ADD UNIQUE KEY uniq_stream_artifacts_stream_kind_name (stream_id, kind, name);
