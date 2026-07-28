ALTER TABLE system_update_runtime_token_rotations
  MODIFY COLUMN staged_token_ciphertext TEXT NULL,
  MODIFY COLUMN staged_token_nonce VARCHAR(128) NULL;

ALTER TABLE system_update_runtime_token_rotations
  DROP CONSTRAINT IF EXISTS ck_system_update_runtime_token_rotations_credential_claim,
  DROP CONSTRAINT IF EXISTS ck_system_update_runtime_token_rotations_secret_material;

UPDATE system_update_runtime_token_rotations
SET staged_token_ciphertext = NULL,
    staged_token_nonce = NULL,
    credential_claim_id_sha256 = NULL,
    credential_claim_revision = NULL
WHERE status IN ('activated','canceled');

ALTER TABLE system_update_runtime_token_rotations
  ADD CONSTRAINT ck_system_update_runtime_token_rotations_credential_claim
    CHECK (
      (
        credential_claim_id_sha256 IS NULL AND
        credential_claim_revision IS NULL AND
        (
          credential_claimed_at IS NULL OR
          status IN ('activated','canceled')
        )
      ) OR (
        credential_claim_id_sha256 IS NOT NULL AND
        credential_claim_revision >= 1 AND
        credential_claimed_at IS NOT NULL
      )
    ),
  ADD CONSTRAINT ck_system_update_runtime_token_rotations_secret_material
    CHECK (
      (
        status IN ('staged','local_staged','heartbeat_proved','cancel_requested') AND
        staged_token_ciphertext IS NOT NULL AND
        staged_token_nonce IS NOT NULL
      ) OR (
        status IN ('activated','canceled') AND
        staged_token_ciphertext IS NULL AND
        staged_token_nonce IS NULL AND
        credential_claim_id_sha256 IS NULL AND
        credential_claim_revision IS NULL
      )
    );
