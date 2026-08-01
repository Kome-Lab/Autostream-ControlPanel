CREATE TABLE IF NOT EXISTS update_agent_target_local_listeners (
  updater_service_id VARCHAR(128) NOT NULL,
  target_id VARCHAR(128) NOT NULL,
  binding_policy_revision BIGINT NOT NULL,
  local_listen_port INT NOT NULL,
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (updater_service_id, target_id),
  CONSTRAINT fk_update_agent_target_local_listeners_policy
    FOREIGN KEY (updater_service_id)
    REFERENCES update_agent_policies(service_id)
    ON DELETE CASCADE,
  CONSTRAINT ck_update_agent_target_local_listeners_revision
    CHECK (binding_policy_revision >= 1),
  CONSTRAINT ck_update_agent_target_local_listeners_port
    CHECK (local_listen_port BETWEEN 1024 AND 65535)
);
