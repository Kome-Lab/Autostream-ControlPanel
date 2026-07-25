CREATE TABLE IF NOT EXISTS update_agent_policies (
  service_id VARCHAR(128) PRIMARY KEY,
  revision BIGINT NOT NULL,
  policy_json JSON NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  CONSTRAINT fk_update_agent_policies_service
    FOREIGN KEY (service_id) REFERENCES services(service_id) ON DELETE CASCADE
);
