package database

import (
	"strings"
	"testing"
)

func TestSystemUpdateRuntimeTokenRotationMigrationIsHostFencedAndSecretSafe(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/052_system_update_runtime_token_rotations.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS system_update_runtime_token_rotations",
		"execution_host_id VARCHAR(191) NOT NULL",
		"active_execution_host_id VARCHAR(191) GENERATED ALWAYS AS",
		"status IN ('staged','local_staged','heartbeat_proved','cancel_requested')",
		"UNIQUE (active_execution_host_id)",
		"UNIQUE (service_id, idempotency_key)",
		"staged_token_hash CHAR(64) NOT NULL",
		"staged_token_ciphertext TEXT NULL",
		"staged_token_nonce VARCHAR(128) NULL",
		"local_stage_receipt_id VARCHAR(128) NULL",
		"credential_claim_id_sha256 CHAR(64) NULL",
		"credential_claim_revision BIGINT NULL",
		"credential_claimed_at DATETIME(6) NULL",
		"local_stage_acknowledged_at DATETIME(6) NULL",
		"heartbeat_proved_at DATETIME(6) NULL",
		"cancel_requested_at DATETIME(6) NULL",
		"cancel_acknowledged_at DATETIME(6) NULL",
		"ck_system_update_runtime_token_rotations_secret_material",
		"status IN ('activated','canceled')",
		"FOREIGN KEY (execution_host_id) REFERENCES system_update_execution_hosts(execution_host_id)",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("runtime token rotation migration is missing %q:\n%s", required, text)
		}
	}
	lower := strings.ToLower(text)
	for _, forbidden := range []string{
		"raw_token",
		"plaintext",
		"runtime_token varchar",
		"runtime_token text",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("runtime token rotation migration persists forbidden secret shape %q:\n%s", forbidden, text)
		}
	}
}

func TestRuntimeTokenRotationTerminalScrubMigrationRemovesReplaySecrets(t *testing.T) {
	body, err := embeddedMigrations.ReadFile(
		"migrations/055_runtime_token_rotation_terminal_scrub.sql",
	)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"MODIFY COLUMN staged_token_ciphertext TEXT NULL",
		"MODIFY COLUMN staged_token_nonce VARCHAR(128) NULL",
		"credential_claim_id_sha256 = NULL",
		"credential_claim_revision = NULL",
		"WHERE status IN ('activated','canceled')",
		"ck_system_update_runtime_token_rotations_secret_material",
		"status IN ('staged','local_staged','heartbeat_proved','cancel_requested')",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("terminal scrub migration is missing %q:\n%s", required, text)
		}
	}
}
