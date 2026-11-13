CREATE TABLE v3_cp_authority_epoch (
 singleton TINYINT NOT NULL PRIMARY KEY,
 generation BIGINT UNSIGNED NOT NULL,
 CHECK (singleton = 1)
) ENGINE=InnoDB;
INSERT INTO v3_cp_authority_epoch(singleton,generation) VALUES(1,0);
CREATE TABLE v3_cp_authority_records (
 kind VARBINARY(16) NOT NULL,
 realm VARBINARY(128) NOT NULL,
 first_id VARBINARY(128) NOT NULL,
 second_id VARBINARY(128) NOT NULL,
 generation BIGINT UNSIGNED NOT NULL,
 record_bytes LONGBLOB NOT NULL,
 updated_at DATETIME(6) NOT NULL,
 PRIMARY KEY(kind,realm,first_id,second_id)
) ENGINE=InnoDB;
CREATE TABLE v3_cp_authority_history (
 generation BIGINT UNSIGNED NOT NULL,
 ordinal INT UNSIGNED NOT NULL,
 kind VARBINARY(16) NOT NULL,
 realm VARBINARY(128) NOT NULL,
 first_id VARBINARY(128) NOT NULL,
 second_id VARBINARY(128) NOT NULL,
 record_bytes LONGBLOB NOT NULL,
 updated_at DATETIME(6) NOT NULL,
 PRIMARY KEY(generation,ordinal)
) ENGINE=InnoDB;
