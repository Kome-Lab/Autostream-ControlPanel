CREATE TABLE v3_cp_authorization_issuances (
  realm VARBINARY(128) NOT NULL,
  request_id VARBINARY(128) NOT NULL,
  execution_key VARBINARY(128) NOT NULL,
  operation_id VARBINARY(128) NOT NULL,
  attempt_id VARBINARY(128) NOT NULL,
  step_id VARBINARY(128) NOT NULL,
  claim_id VARBINARY(128) NOT NULL,
  state VARBINARY(16) NOT NULL,
  evidence_id VARBINARY(128) NULL,
  immutable_bytes MEDIUMBLOB NOT NULL,
  record_bytes MEDIUMBLOB NOT NULL,
  PRIMARY KEY (realm, request_id),
  UNIQUE KEY v3_cp_authorization_execution (realm, execution_key),
  UNIQUE KEY v3_cp_authorization_step (realm, operation_id, attempt_id, step_id),
  UNIQUE KEY v3_cp_authorization_evidence (evidence_id),
  CHECK (state IN ('PENDING', 'ISSUED', 'CLOSED'))
) ENGINE=InnoDB;
