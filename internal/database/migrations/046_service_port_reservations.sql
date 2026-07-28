CREATE TABLE IF NOT EXISTS service_port_reservations (
  execution_host_id VARCHAR(191) NOT NULL,
  network_namespace VARCHAR(128) NOT NULL,
  protocol VARCHAR(3) NOT NULL,
  port INT UNSIGNED NOT NULL,
  service_id VARCHAR(128) NOT NULL,
  service_role VARCHAR(64) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (execution_host_id, network_namespace, protocol, port),
  CHECK (network_namespace = LOWER(network_namespace)),
  CHECK (network_namespace REGEXP '^[a-z0-9][a-z0-9._:-]{0,127}$'),
  CHECK (protocol IN ('tcp','udp')),
  CHECK (port BETWEEN 1024 AND 65535),
  CHECK (service_role REGEXP '^[a-z0-9][a-z0-9._:-]{0,63}$'),
  FOREIGN KEY (execution_host_id) REFERENCES system_update_execution_hosts(execution_host_id) ON DELETE CASCADE,
  FOREIGN KEY (service_id) REFERENCES services(service_id) ON DELETE CASCADE
);

