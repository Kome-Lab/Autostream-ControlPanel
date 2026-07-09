CREATE TABLE IF NOT EXISTS service_metric_snapshots (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  service_id VARCHAR(128) NOT NULL,
  service_type ENUM('discord_bot','encoder_recorder','worker','observability') NOT NULL,
  metric_name VARCHAR(255) NOT NULL,
  status VARCHAR(80) NOT NULL DEFAULT '',
  value DOUBLE NOT NULL,
  observed_at DATETIME NOT NULL,
  INDEX idx_service_metric_snapshots_observed_at (observed_at),
  INDEX idx_service_metric_snapshots_service_observed (service_id, observed_at)
);
