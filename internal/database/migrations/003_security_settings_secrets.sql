CREATE TABLE IF NOT EXISTS system_settings (
  name VARCHAR(80) PRIMARY KEY,
  value_json JSON NOT NULL,
  updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS secrets (
  name VARCHAR(128) PRIMARY KEY,
  ciphertext TEXT NOT NULL,
  nonce VARCHAR(64) NOT NULL,
  value_hash VARCHAR(64) NOT NULL,
  updated_at DATETIME NOT NULL
);
