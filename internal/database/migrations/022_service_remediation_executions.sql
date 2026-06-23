CREATE TABLE IF NOT EXISTS service_remediation_executions (
  action_id VARCHAR(191) PRIMARY KEY,
  incident_id VARCHAR(191) NOT NULL,
  stream_id VARCHAR(191) NOT NULL,
  action VARCHAR(191) NOT NULL,
  executed_at DATETIME(6) NOT NULL
);
