//go:build linux

package httpapi

import (
	"context"
	"encoding/json"
	"github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"strings"
	"testing"
)

func (f *stPortChainCP) create(ctx context.Context, command stPortChainCPCommand) stPortChainCPResponse {
	key := command.IdempotencyKey
	if key == "" {
		return stPortChainCPResponse{ErrorCode: "idempotency_key_required"}
	}
	body, exists := f.createBodies[key]
	if command.Command == "retry_create" && !exists {
		return stPortChainCPResponse{ErrorCode: "immutable_create_unavailable"}
	}
	if !exists {
		status, response, err := f.request(ctx, http.MethodGet, "/system-updates", nil)
		if err != nil || status != http.StatusOK {
			return stPortChainCPResponse{ErrorCode: "canonical_get_failed"}
		}
		var listing struct {
			Targets []systemUpdateTargetResponse `json:"targets"`
		}
		if json.Unmarshal(response, &listing) != nil {
			return stPortChainCPResponse{ErrorCode: "canonical_get_invalid"}
		}
		var target *systemUpdateTargetResponse
		for index := range listing.Targets {
			if listing.Targets[index].TargetID == stPortChainTarget {
				target = &listing.Targets[index]
				break
			}
		}
		if target == nil || target.PortPolicySnapshotID == "" {
			return stPortChainCPResponse{ErrorCode: "baseline_not_ready"}
		}
		desired := target.AppliedConfigRevision
		request := map[string]any{"operation": "port_reconfigure", "protocol_version": 2, "port_contract_version": 2, "mode": command.Mode,
			"target_id": stPortChainTarget, "expected_snapshot_id": target.PortPolicySnapshotID,
			"expected_endpoint_revision": target.EndpointRevision, "fence": target.OwnershipEpoch, "required_capability": "host.port", "idempotency_key": key}
		if f.config.Mode == "docker" {
			if command.LocalPort != 0 || target.PortMapping == nil || target.PortMapping.Mode != "docker" || target.PortMapping.PublishedPort != target.LocalListenPort {
				return stPortChainCPResponse{ErrorCode: "docker_create_baseline_unavailable"}
			}
			if command.PublishedPort != target.PortMapping.PublishedPort || command.ContainerPort != target.PortMapping.ContainerPort {
				desired++
			}
			request["new_published_port"], request["new_container_port"] = command.PublishedPort, command.ContainerPort
		} else {
			if command.PublishedPort != 0 || command.ContainerPort != 0 {
				return stPortChainCPResponse{ErrorCode: "invalid_systemd_create_fields"}
			}
			if command.LocalPort != target.LocalListenPort {
				desired++
			}
			request["new_local_listen_port"] = command.LocalPort
		}
		request["desired_revision"] = desired
		if command.Mode == contracts.SystemUpdatePortModeLocalAndAdvertised {
			request["new_advertised_port"] = command.AdvertisedPort
		}
		body, _ = json.Marshal(request)
		if contracts.ValidateSystemUpdatePortCreateRequest(body) != nil {
			return stPortChainCPResponse{ErrorCode: "invalid_create_intent"}
		}
		f.createBodies[key] = append([]byte(nil), body...)
	}
	f.mu.Lock()
	before := f.fault == "create_before"
	if before {
		f.fault = ""
		f.dropped++
	}
	f.mu.Unlock()
	if before {
		return stPortChainCPResponse{ErrorCode: "response_lost"}
	}
	status, response, err := f.request(ctx, http.MethodPost, "/system-updates", body)
	if err != nil {
		return stPortChainCPResponse{ErrorCode: "response_lost"}
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return stPortChainCreateRejection(status, response)
	}
	var result store.SystemUpdateJob
	if json.Unmarshal(response, &result) != nil || result.ID == "" || result.TargetID != stPortChainTarget {
		return stPortChainCPResponse{ErrorCode: "create_response_invalid"}
	}
	return stPortChainCPResponse{OK: true, Job: response}
}

// Retain only bounded HTTP status and literal CP codes, never the response body.
func stPortChainCreateRejection(status int, body []byte) stPortChainCPResponse {
	result := stPortChainCPResponse{ErrorCode: "create_rejected", FailureStage: "create", FailureHTTPStatus: status, FailureCode: "unknown"}
	if status < 100 || status > 599 {
		result.FailureHTTPStatus = 0
	}
	var wire struct {
		Code string `json:"code"`
	}
	if len(body) > 1<<20 || json.Unmarshal(body, &wire) != nil {
		return result
	}
	switch wire.Code {
	case "invalid_system_update_request",
		"system_update_target_not_found",
		"idempotency_key_conflict",
		"system_update_target_active",
		"system_update_endpoint_revision_conflict",
		"service_port_reserved",
		"system_update_port_store_mismatch",
		"system_update_ownership_conflict",
		"system_update_port_reconfigure_not_ready",
		"create_system_update_failed",
		"system_update_advertised_only_unsupported",
		"system_update_port_contract_required",
		"system_update_port_policy_snapshot_unavailable",
		"system_update_port_snapshot_stale",
		"system_update_port_idempotency_conflict",
		"system_update_port_recovery_required",
		"system_update_host_busy":
		result.FailureCode = wire.Code
	}
	return result
}

func TestSTPortFullChainCreateDiagnosticRetainsOnlyStatusAndCode(t *testing.T) {
	for _, item := range []struct{ body, code string }{
		{`{"code":"system_update_port_reconfigure_not_ready","detail":"untrusted_marker"}`, "system_update_port_reconfigure_not_ready"},
		{`{"code":"untrusted_marker"}`, "unknown"},
		{`{"code":`, "unknown"},
	} {
		got := stPortChainCreateRejection(http.StatusConflict, []byte(item.body))
		encoded, err := json.Marshal(got)
		if err != nil || got.OK || got.ErrorCode != "create_rejected" || got.FailureStage != "create" || got.FailureHTTPStatus != http.StatusConflict || got.FailureCode != item.code || strings.Contains(string(encoded), "untrusted_marker") {
			t.Fatal("create rejection lost its boundary or disclosed an unapproved field")
		}
	}
}
