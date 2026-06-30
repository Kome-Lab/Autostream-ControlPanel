ALTER TABLE webauthn_ceremony_sessions
  DROP FOREIGN KEY fk_webauthn_ceremony_sessions_user;

ALTER TABLE webauthn_ceremony_sessions
  MODIFY user_id CHAR(36) NULL;

ALTER TABLE webauthn_ceremony_sessions
  ADD CONSTRAINT fk_webauthn_ceremony_sessions_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
