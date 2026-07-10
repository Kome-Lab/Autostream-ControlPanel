CREATE TABLE IF NOT EXISTS user_avatars (
    user_id CHAR(36) PRIMARY KEY,
    content_type VARCHAR(32) NOT NULL,
    image_data MEDIUMBLOB NOT NULL,
    fingerprint CHAR(64) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    CONSTRAINT fk_user_avatars_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
