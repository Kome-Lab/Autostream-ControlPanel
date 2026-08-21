CREATE INDEX IF NOT EXISTS idx_service_metric_snapshots_series_observed
  ON service_metric_snapshots (service_id, metric_name, observed_at, id);
