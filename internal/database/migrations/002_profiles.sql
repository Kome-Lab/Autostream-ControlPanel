CREATE TABLE IF NOT EXISTS profiles (
  id CHAR(36) PRIMARY KEY,
  kind ENUM('encoder','archive','caption','overlay','discord_config','youtube_output') NOT NULL,
  name VARCHAR(255) NOT NULL,
  config JSON NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  UNIQUE KEY uniq_profiles_kind_name (kind, name)
);
