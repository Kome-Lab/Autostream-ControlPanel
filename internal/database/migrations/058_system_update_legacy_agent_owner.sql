ALTER TABLE system_update_execution_hosts
  ADD COLUMN IF NOT EXISTS legacy_agent_service_id VARCHAR(128) NULL
    AFTER agent_service_id;

UPDATE system_update_execution_hosts
SET legacy_agent_service_id = agent_service_id
WHERE transport_mode = 'ssh_v1'
  AND legacy_agent_service_id IS NULL;

ALTER TABLE system_update_execution_hosts
  ADD FOREIGN KEY IF NOT EXISTS fk_system_update_execution_hosts_legacy_agent
    (legacy_agent_service_id)
    REFERENCES services(service_id)
    ON DELETE RESTRICT;
