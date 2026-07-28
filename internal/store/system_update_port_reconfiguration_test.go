package store

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSystemUpdatePortReconfigurationTypesKeepPlanAndResultDistinct(t *testing.T) {
	if SystemUpdateOperationSoftwareUpdate != "software_update" ||
		SystemUpdateOperationPortReconfigure != "port_reconfigure" {
		t.Fatal("system-update operation constants changed")
	}
	if SystemUpdateMutationOperationPortReconfigure != "port_reconfigure" ||
		SystemUpdateMutationOperationPortReconfigureReconcile != "port_reconfigure_reconcile" {
		t.Fatal("port-reconfiguration grant action constants changed")
	}
	for name, result := range map[string]SystemUpdatePortReconfigurationResult{
		"applied":         SystemUpdatePortReconfigurationApplied,
		"rolled_back":     SystemUpdatePortReconfigurationRolledBack,
		"unchanged":       SystemUpdatePortReconfigurationUnchanged,
		"rollback_failed": SystemUpdatePortReconfigurationRollbackFailed,
	} {
		if string(result) != name {
			t.Fatalf("port-reconfiguration result %q = %q", name, result)
		}
	}

	plan := SystemUpdatePortReconfiguration{
		NetworkNamespace:               "host",
		Protocol:                       SystemUpdatePortProtocolTCP,
		OldPort:                        8084,
		NewPort:                        18084,
		ExpectedEndpointRevision:       7,
		TargetEndpointRevision:         8,
		ExpectedConfigRevision:         11,
		TargetConfigRevision:           12,
		ExpectedConfigSHA256:           "sha256:" + strings.Repeat("a", 64),
		TargetConfigSHA256:             "sha256:" + strings.Repeat("b", 64),
		ExpectedSourcePolicyRevision:   19,
		ExpectedUpdaterPolicyRevision:  23,
		ExpectedExecutorPolicyRevision: 5,
		ExpectedExecutorPolicySHA256:   "sha256:" + strings.Repeat("c", 64),
		PortPlanSHA256:                 strings.Repeat("d", 64),
	}
	body, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"network_namespace", "protocol", "old_port", "new_port",
		"expected_endpoint_revision", "target_endpoint_revision",
		"expected_config_revision", "target_config_revision",
		"expected_config_sha256", "target_config_sha256",
		"expected_source_policy_revision", "expected_updater_policy_revision", "expected_executor_policy_revision",
		"expected_executor_policy_sha256", "port_plan_sha256",
	} {
		if !strings.Contains(string(body), `"`+field+`"`) {
			t.Fatalf("port plan JSON is missing %q: %s", field, body)
		}
	}
	if strings.Contains(string(body), `"result"`) {
		t.Fatalf("job/grant port plan emitted an absent report result: %s", body)
	}

	resultBody, err := json.Marshal(SystemUpdatePortReconfiguration{
		Result: SystemUpdatePortReconfigurationRolledBack,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(resultBody) != `{"result":"rolled_back"}` {
		t.Fatalf("report result must not leak flat persistence fields: %s", resultBody)
	}
}
