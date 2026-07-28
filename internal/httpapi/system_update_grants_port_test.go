package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestSystemUpdatePortMutationGrantHTTPBindingPreservesExactNestedFences(t *testing.T) {
	body := `{
		"lease_generation": 4,
		"host_id": "host-a",
		"transport_mode": "pull_v2",
		"ownership_epoch": 3,
		"policy_revision": 13,
		"target_id": "worker-01",
		"service_type": "worker",
		"target_version": "v1.2.3",
		"deployment_mode": "systemd",
		"job_operation": "port_reconfigure",
		"operation": "port_reconfigure",
		"plan_sha256": "` + strings.Repeat("d", 64) + `",
		"session_id": "session-port-http-0001",
		"port_reconfigure": {
			"network_namespace": "host",
			"protocol": "tcp",
			"old_port": 8081,
			"new_port": 18081,
			"expected_endpoint_revision": 4,
			"target_endpoint_revision": 5,
			"expected_config_revision": 7,
			"target_config_revision": 8,
			"expected_config_sha256": "sha256:` + strings.Repeat("a", 64) + `",
			"target_config_sha256": "sha256:` + strings.Repeat("b", 64) + `",
			"expected_source_policy_revision": 11,
			"expected_updater_policy_revision": 13,
			"expected_executor_policy_revision": 17,
			"expected_executor_policy_sha256": "sha256:` + strings.Repeat("c", 64) + `",
			"port_plan_sha256": "` + strings.Repeat("d", 64) + `"
		}
	}`
	request := httptest.NewRequest(http.MethodPost, "/mutation-grants/consume", strings.NewReader(body))
	var decoded systemUpdateMutationGrantConsumeBody
	if !decodeSingleSystemUpdateGrantJSON(request, &decoded) {
		t.Fatal("exact port mutation-grant body was rejected")
	}
	binding := decoded.systemUpdateMutationGrantBindingBody.storeBinding()
	if decoded.LeaseGeneration != 4 ||
		binding.TargetServiceType != "worker" ||
		binding.JobOperation != store.SystemUpdateOperationPortReconfigure ||
		binding.PortReconfigure == nil ||
		binding.PortReconfigure.OldPort != 8081 ||
		binding.PortReconfigure.NewPort != 18081 ||
		binding.PortReconfigure.ExpectedSourcePolicyRevision != 11 ||
		binding.PortReconfigure.ExpectedUpdaterPolicyRevision != 13 ||
		binding.PortReconfigure.ExpectedExecutorPolicyRevision != 17 ||
		binding.PortReconfigure.PortPlanSHA256 != binding.PlanSHA256 {
		t.Fatalf("mapped port mutation-grant binding = %#v", binding)
	}

	metadata := systemUpdateMutationGrantAuditMetadata(binding)
	portMetadata, ok := metadata["port_reconfigure"].(map[string]any)
	if !ok ||
		metadata["service_type"] != "worker" ||
		metadata["job_operation"] != store.SystemUpdateOperationPortReconfigure ||
		portMetadata["old_port"] != 8081 ||
		portMetadata["new_port"] != 18081 ||
		portMetadata["expected_source_policy_revision"] != int64(11) {
		t.Fatalf("port mutation-grant audit metadata = %#v", metadata)
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"lease_token", "grant_token", "runtime_token", "release_token"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("audit metadata contains secret-bearing field %q: %s", forbidden, encoded)
		}
	}
}

func TestSystemUpdatePortMutationGrantHTTPRejectsUnknownNestedFields(t *testing.T) {
	body := `{
		"lease_generation": 4,
		"host_id": "host-a",
		"transport_mode": "pull_v2",
		"ownership_epoch": 3,
		"policy_revision": 13,
		"target_id": "worker-01",
		"service_type": "worker",
		"target_version": "v1.2.3",
		"deployment_mode": "systemd",
		"job_operation": "port_reconfigure",
		"operation": "port_reconfigure",
		"plan_sha256": "` + strings.Repeat("d", 64) + `",
		"session_id": "session-port-http-0001",
		"port_reconfigure": {
			"network_namespace": "host",
			"protocol": "tcp",
			"old_port": 8081,
			"new_port": 18081,
			"expected_endpoint_revision": 4,
			"target_endpoint_revision": 5,
			"expected_config_revision": 7,
			"target_config_revision": 8,
			"expected_config_sha256": "sha256:` + strings.Repeat("a", 64) + `",
			"target_config_sha256": "sha256:` + strings.Repeat("b", 64) + `",
			"expected_source_policy_revision": 11,
			"expected_updater_policy_revision": 13,
			"expected_executor_policy_revision": 17,
			"expected_executor_policy_sha256": "sha256:` + strings.Repeat("c", 64) + `",
			"port_plan_sha256": "` + strings.Repeat("d", 64) + `",
			"unit": "attacker-selected.service"
		}
	}`
	request := httptest.NewRequest(http.MethodPost, "/mutation-grants/consume", strings.NewReader(body))
	var decoded systemUpdateMutationGrantConsumeBody
	if decodeSingleSystemUpdateGrantJSON(request, &decoded) {
		t.Fatal("unknown privileged nested field was accepted")
	}
}

func TestSystemUpdateMutationGrantHTTPRejectsOversizedTrailingBody(t *testing.T) {
	body := `{"lease_generation":1}` + strings.Repeat(" ", 64*1024) + "x"
	request := httptest.NewRequest(
		http.MethodPost,
		"/mutation-grants/consume",
		strings.NewReader(body),
	)
	var decoded systemUpdateMutationGrantConsumeBody
	if decodeSingleSystemUpdateGrantJSON(request, &decoded) {
		t.Fatal("oversized mutation-grant body was accepted after a valid JSON prefix")
	}
}
