package store

import (
	"bytes"
	"os"
)

// These readers bind the existing lock-order assertions to the real production
// owners. They retain declaration order used by the existing source delimiters;
// neither production declarations nor expected mutation results are copied here.
func readServiceRegistryLockOrderSource() ([]byte, error) {
	return readStoreLockOrderSources(
		"services.go",
		"service_token_references_mariadb.go",
		"service_tokens_mariadb.go",
		"service_registration_mariadb.go",
		"service_reports_mariadb.go",
		"service_node_configuration_mariadb.go",
		"service_catalog_mariadb.go",
		"service_assignments_events_mariadb.go",
		"service_rows_mariadb.go",
		"service_registration_validation.go",
	)
}

func readUpdaterPolicyLockOrderSource() ([]byte, error) {
	return readStoreLockOrderSources(
		"updater_policy.go",
		"updater_policy_memory.go",
		"updater_policy_mariadb.go",
		"updater_ownership_locks_mariadb.go",
		"updater_ownership_activate_mariadb.go",
		"updater_ownership_deactivate_mariadb.go",
		"updater_policy_projections_mariadb.go",
		"updater_activation_readiness.go",
	)
}

func readFIX005LockOrderSource() ([]byte, error) {
	return readStoreLockOrderSources(
		"service_token_lock_order_fix005_mariadb_test.go",
		"service_token_lock_order_fix005_pull_fixture_test.go",
		"service_token_lock_order_fix005_precreate_activation_test.go",
		"service_token_lock_order_fix005_policy_cycles_test.go",
		"service_token_lock_order_fix005_runtime_matrix_test.go",
		"service_token_lock_order_fix005_ownership_runtime_test.go",
		"service_token_lock_order_fix005_cleanup_test.go",
	)
}

func readStoreLockOrderSources(paths ...string) ([]byte, error) {
	var source bytes.Buffer
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		source.Write(body)
		source.WriteByte('\n')
	}
	return source.Bytes(), nil
}
