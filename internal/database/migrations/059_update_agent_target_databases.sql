CREATE TABLE IF NOT EXISTS update_agent_target_databases (
  updater_service_id VARCHAR(128) NOT NULL,
  target_id VARCHAR(128) NOT NULL,
  binding_policy_revision BIGINT NOT NULL,
  database_name VARCHAR(64) NOT NULL,
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (updater_service_id, target_id),
  CONSTRAINT fk_update_agent_target_databases_policy
    FOREIGN KEY (updater_service_id)
    REFERENCES update_agent_policies(service_id)
    ON DELETE CASCADE,
  CONSTRAINT ck_update_agent_target_databases_revision
    CHECK (binding_policy_revision >= 1),
  CONSTRAINT ck_update_agent_target_databases_name
    CHECK (database_name REGEXP '^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$')
);
