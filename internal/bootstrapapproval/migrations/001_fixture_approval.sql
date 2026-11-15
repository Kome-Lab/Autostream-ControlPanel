CREATE TABLE v3_cp_approval_checkpoint (
 singleton TINYINT NOT NULL PRIMARY KEY,
 revision BIGINT UNSIGNED NOT NULL,
 manifest_id VARBINARY(128) NOT NULL,
 attempt_id VARBINARY(128) NOT NULL,
 request_binding VARBINARY(64) NOT NULL,
 generation BIGINT UNSIGNED NOT NULL,
 CHECK(singleton=1)
) ENGINE=InnoDB
-- statement
INSERT INTO v3_cp_approval_checkpoint VALUES(1,0,'','','',0)
-- statement
CREATE TABLE v3_cp_approval_consumptions (
 manifest_id VARBINARY(128) NOT NULL PRIMARY KEY,
 attempt_id VARBINARY(128) NOT NULL UNIQUE,
 event_id VARBINARY(128) NOT NULL,
 actor VARBINARY(128) NOT NULL,
 purpose VARBINARY(32) NOT NULL,
 request_binding VARBINARY(64) NOT NULL,
 origin_id VARBINARY(128) NOT NULL,
 origin_receipt VARBINARY(128) NOT NULL,
 origin_revision BIGINT UNSIGNED NOT NULL,
 ceiling_bytes BLOB NOT NULL,
 generation BIGINT UNSIGNED NOT NULL,
 checkpoint_revision BIGINT UNSIGNED NOT NULL UNIQUE,
 CHECK(purpose='APPROVAL_LOCAL'),
 CHECK(origin_revision>0),
 CHECK(generation>0)
) ENGINE=InnoDB
-- statement
CREATE TABLE v3_cp_approval_history (
 revision BIGINT UNSIGNED NOT NULL PRIMARY KEY,
 manifest_id VARBINARY(128) NOT NULL UNIQUE,
 attempt_id VARBINARY(128) NOT NULL UNIQUE,
 request_binding VARBINARY(64) NOT NULL,
 generation BIGINT UNSIGNED NOT NULL
) ENGINE=InnoDB
-- statement
CREATE TRIGGER v3_approval_consumption_no_update BEFORE UPDATE ON v3_cp_approval_consumptions FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='immutable approval consumption'
-- statement
CREATE TRIGGER v3_approval_consumption_no_delete BEFORE DELETE ON v3_cp_approval_consumptions FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='immutable approval consumption'
-- statement
CREATE TRIGGER v3_approval_history_no_update BEFORE UPDATE ON v3_cp_approval_history FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='immutable approval history'
-- statement
CREATE TRIGGER v3_approval_history_no_delete BEFORE DELETE ON v3_cp_approval_history FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='immutable approval history'
