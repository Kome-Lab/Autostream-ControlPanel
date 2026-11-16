CREATE TABLE operational_authorization_epoch (
 singleton TINYINT UNSIGNED NOT NULL PRIMARY KEY,
 generation BIGINT UNSIGNED NOT NULL,
 CONSTRAINT operational_epoch_singleton CHECK (singleton = 1),
 CONSTRAINT operational_epoch_positive CHECK (generation > 0)
) ENGINE=InnoDB;
-- statement
CREATE TABLE operational_authorization_history (
 generation BIGINT UNSIGNED NOT NULL PRIMARY KEY,
 source_table VARCHAR(32) NOT NULL,
 source_event VARCHAR(16) NOT NULL,
 source_key VARCHAR(160) NOT NULL,
 change_digest CHAR(64) NOT NULL
) ENGINE=InnoDB;
-- statement
INSERT INTO operational_authorization_epoch (singleton, generation) VALUES (1, 1);
-- statement
INSERT INTO operational_authorization_history (generation, source_table, source_event, source_key, change_digest) VALUES (1, 'schema', 'migration', '1', SHA2('s13-operational-authorization-v1', 256));
