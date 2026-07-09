CREATE TABLE IF NOT EXISTS email_change_challenges (
  id CHAR(64) PRIMARY KEY,
  user_id CHAR(36) NOT NULL,
  email VARCHAR(255) NOT NULL,
  expires_at DATETIME NOT NULL,
  created_at DATETIME NOT NULL,
  INDEX idx_email_change_challenges_user_id (user_id),
  INDEX idx_email_change_challenges_expires_at (expires_at),
  CONSTRAINT fk_email_change_challenges_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
