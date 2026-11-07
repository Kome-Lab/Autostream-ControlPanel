-- Keep the first saved-Node transition closed until the installing update has
-- durable terminal success. Existing execution and update authorities are not
-- rewritten. The singleton is initialized by the application transaction.
CREATE TABLE IF NOT EXISTS stream_node_selection_activation (
 singleton_id TINYINT UNSIGNED NOT NULL PRIMARY KEY,
 activated_at DATETIME(6) NULL,
 update_job_id CHAR(36) NULL,
 target_version VARCHAR(128) NOT NULL DEFAULT '',
 build_commit CHAR(40) NOT NULL DEFAULT '',
 agent_service_id VARCHAR(191) NOT NULL DEFAULT '',
 execution_host_id VARCHAR(191) NOT NULL DEFAULT '',
 ownership_epoch BIGINT NOT NULL DEFAULT 0,
 current_version VARCHAR(128) NOT NULL DEFAULT ''
);
