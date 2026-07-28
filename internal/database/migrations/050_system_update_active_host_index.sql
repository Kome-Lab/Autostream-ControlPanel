CREATE INDEX IF NOT EXISTS idx_system_update_jobs_execution_host_status_created
  ON system_update_jobs (execution_host_id, status, created_at);
