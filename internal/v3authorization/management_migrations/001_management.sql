CREATE TABLE v3_cp_management_bootstrap (
 singleton TINYINT NOT NULL PRIMARY KEY,
 state VARBINARY(32) NOT NULL,
 event_id VARBINARY(128) NOT NULL,
 principal VARBINARY(128) NOT NULL,
 generation BIGINT UNSIGNED NOT NULL,
 CHECK(singleton=1)
) ENGINE=InnoDB
-- statement
INSERT INTO v3_cp_management_bootstrap VALUES(1,'UNINITIALIZED','','',0)
-- statement
CREATE TABLE v3_cp_management_grants (
 realm VARBINARY(128) NOT NULL,host VARBINARY(128) NOT NULL,
 resource VARBINARY(128) NOT NULL,backend VARBINARY(128) NOT NULL,
 action VARBINARY(64) NOT NULL,principal VARBINARY(128) NOT NULL,
 generation BIGINT UNSIGNED NOT NULL,grant_bytes LONGBLOB NOT NULL,
 PRIMARY KEY(realm,host,resource,backend,action,principal)
) ENGINE=InnoDB
-- statement
CREATE TABLE v3_cp_management_grant_history (
 generation BIGINT UNSIGNED NOT NULL,ordinal INT UNSIGNED NOT NULL,
 grant_bytes LONGBLOB NOT NULL,
 PRIMARY KEY(generation,ordinal)
) ENGINE=InnoDB
-- statement
CREATE TABLE v3_cp_management_audit (
 audit_id VARBINARY(32) NOT NULL PRIMARY KEY,
 event_id VARBINARY(128) NOT NULL,observed_at DATETIME(6) NOT NULL,
 time_qualification VARBINARY(32) NOT NULL,
 actor VARBINARY(128) NOT NULL,operation VARBINARY(64) NOT NULL,
 target_type VARBINARY(32) NOT NULL,target_id VARBINARY(260) NOT NULL,
 realm VARBINARY(128) NOT NULL,host VARBINARY(128) NOT NULL,
 resource VARBINARY(128) NOT NULL,backend VARBINARY(128) NOT NULL,
 action VARBINARY(64) NOT NULL,
 before_revision BIGINT UNSIGNED NOT NULL,after_revision BIGINT UNSIGNED NOT NULL,
 result VARBINARY(32) NOT NULL,reason_code VARBINARY(64) NOT NULL,
 correlation_id VARBINARY(128) NOT NULL,
 INDEX v3_management_event(actor,event_id,result)
) ENGINE=InnoDB
-- statement
CREATE TRIGGER v3_management_audit_no_update BEFORE UPDATE ON v3_cp_management_audit FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='append-only audit'
-- statement
CREATE TRIGGER v3_management_audit_no_delete BEFORE DELETE ON v3_cp_management_audit FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='append-only audit'
-- statement
CREATE TRIGGER v3_management_history_no_update BEFORE UPDATE ON v3_cp_management_grant_history FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='append-only history'
-- statement
CREATE TRIGGER v3_management_history_no_delete BEFORE DELETE ON v3_cp_management_grant_history FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='append-only history'
-- statement
CREATE TRIGGER v3_management_bootstrap_no_delete BEFORE DELETE ON v3_cp_management_bootstrap FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='bootstrap marker retained'
-- statement
CREATE TRIGGER v3_management_bootstrap_one_way BEFORE UPDATE ON v3_cp_management_bootstrap FOR EACH ROW
BEGIN
 IF OLD.state <> 'UNINITIALIZED' OR NEW.state <> 'INITIALIZED' OR OLD.event_id <> '' OR OLD.principal <> '' OR OLD.generation <> 0 OR NEW.event_id = '' OR NEW.principal = '' OR NEW.generation = 0 THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='bootstrap is one-way';
 END IF;
END
