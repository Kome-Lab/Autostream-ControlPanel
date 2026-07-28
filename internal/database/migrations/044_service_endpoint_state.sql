ALTER TABLE services
  ADD COLUMN IF NOT EXISTS transport_mode VARCHAR(16) NULL AFTER description,
  ADD COLUMN IF NOT EXISTS execution_host_id VARCHAR(191) NULL AFTER transport_mode,
  ADD COLUMN IF NOT EXISTS ownership_epoch BIGINT NOT NULL DEFAULT 0 AFTER execution_host_id,
  ADD COLUMN IF NOT EXISTS desired_host VARCHAR(255) NULL AFTER public_url,
  ADD COLUMN IF NOT EXISTS desired_port INT NULL AFTER desired_host,
  ADD COLUMN IF NOT EXISTS desired_ssl_enabled BOOLEAN NULL AFTER desired_port,
  ADD COLUMN IF NOT EXISTS desired_public_url TEXT NULL AFTER desired_ssl_enabled,
  ADD COLUMN IF NOT EXISTS reported_api_host VARCHAR(255) NULL AFTER desired_public_url,
  ADD COLUMN IF NOT EXISTS reported_api_port INT NULL AFTER reported_api_host,
  ADD COLUMN IF NOT EXISTS reported_api_ssl_enabled BOOLEAN NULL AFTER reported_api_port,
  ADD COLUMN IF NOT EXISTS reported_api_public_url TEXT NULL AFTER reported_api_ssl_enabled,
  ADD COLUMN IF NOT EXISTS endpoint_revision BIGINT NOT NULL DEFAULT 1 AFTER reported_api_public_url,
  ADD COLUMN IF NOT EXISTS endpoint_status VARCHAR(32) NOT NULL DEFAULT 'applied' AFTER endpoint_revision;

UPDATE services
SET desired_host = host,
    desired_port = port,
    desired_ssl_enabled = ssl_enabled,
    desired_public_url = public_url
WHERE desired_host IS NULL
  AND desired_port IS NULL
  AND desired_ssl_enabled IS NULL
  AND desired_public_url IS NULL;

UPDATE services
SET transport_mode = 'ssh_v1'
WHERE service_type = 'update_agent'
  AND (transport_mode IS NULL OR transport_mode = '');

ALTER TABLE services
  ADD COLUMN IF NOT EXISTS pull_agent_execution_host_id VARCHAR(191) GENERATED ALWAYS AS (
    CASE
      WHEN service_type = 'update_agent' AND transport_mode = 'pull_v2' THEN execution_host_id
      ELSE NULL
    END
  ) STORED;

CREATE UNIQUE INDEX IF NOT EXISTS uq_services_execution_host
  ON services (pull_agent_execution_host_id);
