CREATE TABLE IF NOT EXISTS system_update_execution_hosts (
  execution_host_id VARCHAR(191) PRIMARY KEY,
  transport_mode VARCHAR(16) NOT NULL,
  agent_service_id VARCHAR(128) NOT NULL,
  ownership_epoch BIGINT NOT NULL,
  policy_revision BIGINT NOT NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  CHECK (transport_mode IN ('ssh_v1','pull_v2')),
  CHECK (ownership_epoch > 0),
  CHECK (policy_revision >= 0),
  FOREIGN KEY (agent_service_id) REFERENCES services(service_id) ON DELETE RESTRICT
);

