package store

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
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

var runtimeTokenLockOrderOwners = []string{
	"system_update_runtime_token_rotations_mariadb.go",
	"runtime_token_rotation_activation_mariadb.go",
	"runtime_token_rotation_cancel_ack_mariadb.go",
	"runtime_token_rotation_claim_mariadb.go",
	"runtime_token_rotation_emergency_mariadb.go",
	"runtime_token_rotation_proof_mariadb.go",
	"runtime_token_rotation_rows_mariadb.go",
	"runtime_token_rotation_stage_mariadb.go",
}

var runtimeTokenLockOrderMethods = []string{
	"stageSystemUpdateRuntimeTokenRotationOnce",
	"ClaimSystemUpdateRuntimeTokenRotationStagedCredential",
	"MarkSystemUpdateRuntimeTokenRotationLocalStaged",
	"ProveSystemUpdateRuntimeTokenRotationHeartbeat",
	"ActivateSystemUpdateRuntimeTokenRotation",
	"CancelSystemUpdateRuntimeTokenRotation",
	"AcknowledgeSystemUpdateRuntimeTokenRotationCancel",
	"EmergencyRevokeSystemUpdateRuntimeToken",
}

// Resolve each method inside its actual owner, without depending on a following
// declaration that may now live in another file. readFile permits memory-only
// mutants of these same production sources in the reader's direct regressions.
func readRuntimeTokenLockOrderSource(readFile func(string) ([]byte, error)) (map[string]string, error) {
	bodies := make(map[string]string, len(runtimeTokenLockOrderMethods))
	for _, name := range runtimeTokenLockOrderMethods {
		bodies[name] = ""
	}
	for _, path := range runtimeTokenLockOrderOwners {
		source, err := readFile(path)
		if err != nil {
			return nil, fmt.Errorf("runtime token owner %s: %w", path, err)
		}
		positions := token.NewFileSet()
		parsed, err := parser.ParseFile(positions, path, source, 0)
		if err != nil {
			return nil, fmt.Errorf("runtime token owner %s: %w", path, err)
		}
		if parsed.Name.Name != "store" {
			return nil, fmt.Errorf("runtime token owner %s has package %s, want store", path, parsed.Name.Name)
		}
		for _, declaration := range parsed.Decls {
			method, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			name := method.Name.Name
			body, wanted := bodies[name]
			if !wanted {
				continue
			}
			if method.Recv == nil || len(method.Recv.List) != 1 {
				return nil, fmt.Errorf("runtime token method %s must have receiver *MariaDBSystemUpdateStore", name)
			}
			pointer, ok := method.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				return nil, fmt.Errorf("runtime token method %s must have receiver *MariaDBSystemUpdateStore", name)
			}
			receiver, ok := pointer.X.(*ast.Ident)
			if !ok || receiver.Name != "MariaDBSystemUpdateStore" {
				return nil, fmt.Errorf("runtime token method %s must have receiver *MariaDBSystemUpdateStore", name)
			}
			if body != "" {
				return nil, fmt.Errorf("duplicate runtime token method %s", name)
			}
			if method.Body == nil {
				return nil, fmt.Errorf("runtime token method %s has no body", name)
			}
			file := positions.File(method.Pos())
			bodies[name] = string(source[file.Offset(method.Body.Pos()):file.Offset(method.Body.End())])
		}
	}
	for _, name := range runtimeTokenLockOrderMethods {
		if bodies[name] == "" {
			return nil, fmt.Errorf("runtime token method %s not found", name)
		}
	}
	return bodies, nil
}
