package database

import (
	"strings"
	"testing"
)

func TestSystemUpdateExecutionHostsMigrationStoresFencedOwnershipWithoutSecrets(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/045_system_update_execution_hosts.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS system_update_execution_hosts",
		"execution_host_id VARCHAR(191) PRIMARY KEY",
		"transport_mode VARCHAR(16) NOT NULL",
		"agent_service_id VARCHAR(128) NOT NULL",
		"ownership_epoch BIGINT NOT NULL",
		"policy_revision BIGINT NOT NULL",
		"CHECK (transport_mode IN ('ssh_v1','pull_v2'))",
		"FOREIGN KEY (agent_service_id) REFERENCES services(service_id) ON DELETE RESTRICT",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("execution host ownership migration is missing %q:\n%s", required, text)
		}
	}
	assertMigrationContainsNoRawSecrets(t, text)
}

func TestServicePortReservationsMigrationUsesExactHostNamespaceProtocolPortTuple(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/046_service_port_reservations.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS service_port_reservations",
		"execution_host_id VARCHAR(191) NOT NULL",
		"network_namespace VARCHAR(128) NOT NULL",
		"protocol VARCHAR(3) NOT NULL",
		"port INT UNSIGNED NOT NULL",
		"service_id VARCHAR(128) NOT NULL",
		"service_role VARCHAR(64) NOT NULL",
		"PRIMARY KEY (execution_host_id, network_namespace, protocol, port)",
		"CHECK (protocol IN ('tcp','udp'))",
		"CHECK (port BETWEEN 1024 AND 65535)",
		"FOREIGN KEY (execution_host_id) REFERENCES system_update_execution_hosts(execution_host_id) ON DELETE CASCADE",
		"FOREIGN KEY (service_id) REFERENCES services(service_id) ON DELETE CASCADE",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("service port reservation migration is missing %q:\n%s", required, text)
		}
	}
	assertMigrationContainsNoRawSecrets(t, text)
}

func assertMigrationContainsNoRawSecrets(t *testing.T, text string) {
	t.Helper()
	lower := strings.ToLower(text)
	for _, forbidden := range []string{
		"runtime_token",
		"configure_token",
		"activation_token",
		"release_token",
		"github_token",
		"private_key",
		"password",
		"secret",
		"ciphertext",
		"token_hash",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("migration must not persist raw secret material %q:\n%s", forbidden, text)
		}
	}
}
