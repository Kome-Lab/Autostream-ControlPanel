CREATE TABLE IF NOT EXISTS user_admin_guard (
    singleton TINYINT UNSIGNED NOT NULL PRIMARY KEY,
    CONSTRAINT user_admin_guard_singleton CHECK (singleton = 1)
) ENGINE=InnoDB;

INSERT INTO user_admin_guard (singleton) VALUES (1)
ON DUPLICATE KEY UPDATE singleton = VALUES(singleton);
