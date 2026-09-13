package store

import (
	"strings"
	"testing"
)

type mariaDBFIX008PolicyMutationCase struct {
	name            string
	diagnosticField string
	mutate          func(*mariaDBFIX007OwnershipSemanticSnapshot)
}

func mariaDBFIX008PolicyOracleFixture() mariaDBFIX007OwnershipSemanticSnapshot {
	const (
		hostID           = "host-1"
		pullServiceID    = "pull-agent"
		targetServiceID  = "target-service"
		currentTokenID   = "current-token"
		stagedPreviousID = "staged-previous-token"
		stagedTokenID    = "staged-token"
	)
	return mariaDBFIX007OwnershipSemanticSnapshot{
		hosts: map[string]SystemUpdateExecutionHost{
			hostID: {
				ExecutionHostID: hostID, TransportMode: SystemUpdateTransportPullV2,
				AgentServiceID: pullServiceID,
				OwnershipEpoch: 11, PolicyRevision: 23,
			},
		},
		services: map[string]RegisteredService{
			pullServiceID: {
				ServiceID: pullServiceID, ServiceType: "update_agent",
				ExecutionHostID: hostID, TransportMode: SystemUpdateTransportPullV2,
				OwnershipEpoch: 11, TokenID: currentTokenID,
				StagedNodePreviousTokenID: stagedPreviousID, StagedNodeTokenID: stagedTokenID,
			},
			targetServiceID: {
				ServiceID: targetServiceID, ServiceType: "control_panel",
				ExecutionHostID: hostID, TransportMode: SystemUpdateTransportPullV2,
			},
		},
		policies: map[string]UpdaterPolicy{
			pullServiceID: {
				UpdaterID: pullServiceID, Revision: 19, ProjectionRevision: 23,
				LocalExecutorPolicyRevision: 29, TransportMode: SystemUpdateTransportPullV2,
				ExecutionHostID: hostID, LocalExecutorPolicySHA256: strings.Repeat("a", 64),
			},
		},
		tokens: map[string]ServiceToken{
			currentTokenID:   {ID: currentTokenID, ServiceType: "update_agent"},
			stagedPreviousID: {ID: stagedPreviousID, ServiceType: "update_agent"},
			stagedTokenID:    {ID: stagedTokenID, ServiceType: "update_agent"},
		},
		fixtureIDs: map[string]struct{}{
			pullServiceID: {}, targetServiceID: {},
		},
		relevantIDs: map[string]struct{}{
			currentTokenID: {}, stagedPreviousID: {}, stagedTokenID: {},
		},
	}
}

func mariaDBFIX008PolicyMutationMatrix() []mariaDBFIX008PolicyMutationCase {
	return []mariaDBFIX008PolicyMutationCase{
		{
			name: "wrong AgentServiceID", diagnosticField: "host.host-1.agent_service_id",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				host := actual.hosts["host-1"]
				host.AgentServiceID = "legacy-agent"
				actual.hosts["host-1"] = host
			},
		},
		{
			name: "wrong host OwnershipEpoch", diagnosticField: "host.host-1.ownership_epoch",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				host := actual.hosts["host-1"]
				host.OwnershipEpoch--
				actual.hosts["host-1"] = host
			},
		},
		{
			name: "wrong host PolicyRevision", diagnosticField: "host.host-1.policy_revision",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				host := actual.hosts["host-1"]
				host.PolicyRevision--
				actual.hosts["host-1"] = host
			},
		},
		{
			name: "wrong service OwnershipEpoch", diagnosticField: "service.pull-agent.ownership_epoch",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				service := actual.services["pull-agent"]
				service.OwnershipEpoch--
				actual.services["pull-agent"] = service
			},
		},
		{
			name: "wrong service ExecutionHostID", diagnosticField: "service.pull-agent.execution_host_id",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				service := actual.services["pull-agent"]
				service.ExecutionHostID = "wrong-host"
				actual.services["pull-agent"] = service
			},
		},
		{
			name: "wrong current token ref", diagnosticField: "service.pull-agent.current_token_id",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				service := actual.services["pull-agent"]
				service.TokenID = "unexpected-current-token"
				actual.services["pull-agent"] = service
			},
		},
		{
			name: "wrong staged token ref", diagnosticField: "service.pull-agent.staged_token_id",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				service := actual.services["pull-agent"]
				service.StagedNodeTokenID = "unexpected-staged-token"
				actual.services["pull-agent"] = service
			},
		},
		{
			name: "multiple active policies", diagnosticField: "host.host-1.active_policy_binding",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				actual.services["second-pull-agent"] = RegisteredService{
					ServiceID: "second-pull-agent", ServiceType: "update_agent",
					ExecutionHostID: "host-1", TransportMode: SystemUpdateTransportPullV2,
					OwnershipEpoch: 11, TokenID: "second-token",
				}
				actual.policies["second-pull-agent"] = UpdaterPolicy{
					UpdaterID: "second-pull-agent", Revision: 31, ProjectionRevision: 37,
					TransportMode: SystemUpdateTransportPullV2, ExecutionHostID: "host-1",
				}
				actual.tokens["second-token"] = ServiceToken{ID: "second-token", ServiceType: "update_agent"}
			},
		},
		{
			name: "wrong token revoked state", diagnosticField: "token.current-token.revoked",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				token := actual.tokens["current-token"]
				token.RevokedAt = nonNilMariaDBFIX007Time()
				actual.tokens["current-token"] = token
			},
		},
		{
			name: "wrong token service type", diagnosticField: "token.current-token.service_type",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				token := actual.tokens["current-token"]
				token.ServiceType = "worker"
				actual.tokens["current-token"] = token
			},
		},
		{
			name: "missing service-token reference", diagnosticField: "token.current-token.references",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				service := actual.services["pull-agent"]
				service.TokenID = ""
				actual.services["pull-agent"] = service
			},
		},
		{
			name: "unexpected shared service-token reference", diagnosticField: "token.current-token.references",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				actual.services["unexpected-service"] = RegisteredService{
					ServiceID: "unexpected-service", ServiceType: "update_agent", TokenID: "current-token",
				}
			},
		},
		{
			name: "wrong policy revision", diagnosticField: "policy.pull-agent.revision",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				policy := actual.policies["pull-agent"]
				policy.Revision--
				actual.policies["pull-agent"] = policy
			},
		},
		{
			name: "wrong execution host ID", diagnosticField: "host.host-1.execution_host_id",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				host := actual.hosts["host-1"]
				host.ExecutionHostID = "wrong-host"
				actual.hosts["host-1"] = host
			},
		},
		{
			name: "wrong host transport mode", diagnosticField: "host.host-1.transport_mode",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				host := actual.hosts["host-1"]
				host.TransportMode = "unsupported"
				actual.hosts["host-1"] = host
			},
		},
		{
			name: "active owner policy missing", diagnosticField: "host.host-1.policy_revision_closure",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				host := actual.hosts["host-1"]
				host.AgentServiceID = "legacy-agent"
				actual.hosts["host-1"] = host
			},
		},
		{
			name: "wrong service ID", diagnosticField: "service.pull-agent.service_id",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				service := actual.services["pull-agent"]
				service.ServiceID = "wrong-service"
				actual.services["pull-agent"] = service
			},
		},
		{
			name: "wrong service type", diagnosticField: "service.pull-agent.service_type",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				service := actual.services["pull-agent"]
				service.ServiceType = "worker"
				actual.services["pull-agent"] = service
			},
		},
		{
			name: "wrong service transport mode", diagnosticField: "service.pull-agent.transport_mode",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				service := actual.services["pull-agent"]
				service.TransportMode = "unsupported"
				actual.services["pull-agent"] = service
			},
		},
		{
			name: "wrong staged previous token ref", diagnosticField: "service.pull-agent.staged_previous_token_id",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				service := actual.services["pull-agent"]
				service.StagedNodePreviousTokenID = "wrong-staged-previous"
				actual.services["pull-agent"] = service
			},
		},
		{
			name: "wrong policy updater ID", diagnosticField: "policy.pull-agent.updater_id",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				policy := actual.policies["pull-agent"]
				policy.UpdaterID = "wrong-updater"
				actual.policies["pull-agent"] = policy
			},
		},
		{
			name: "wrong policy projection revision", diagnosticField: "policy.pull-agent.projection_revision",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				policy := actual.policies["pull-agent"]
				policy.ProjectionRevision--
				actual.policies["pull-agent"] = policy
			},
		},
		{
			name: "wrong local executor policy revision", diagnosticField: "policy.pull-agent.local_executor_policy_revision",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				policy := actual.policies["pull-agent"]
				policy.LocalExecutorPolicyRevision--
				actual.policies["pull-agent"] = policy
			},
		},
		{
			name: "wrong policy transport mode", diagnosticField: "policy.pull-agent.transport_mode",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				policy := actual.policies["pull-agent"]
				policy.TransportMode = "unsupported"
				actual.policies["pull-agent"] = policy
			},
		},
		{
			name: "wrong policy execution host", diagnosticField: "policy.pull-agent.execution_host_id",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				policy := actual.policies["pull-agent"]
				policy.ExecutionHostID = "wrong-host"
				actual.policies["pull-agent"] = policy
			},
		},
		{
			name: "wrong policy digest", diagnosticField: "policy.pull-agent.local_executor_policy_sha256",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				policy := actual.policies["pull-agent"]
				policy.LocalExecutorPolicySHA256 = strings.Repeat("b", 64)
				actual.policies["pull-agent"] = policy
			},
		},
		{
			name: "wrong token ID", diagnosticField: "token.current-token.id",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				token := actual.tokens["current-token"]
				token.ID = "wrong-token-id"
				actual.tokens["current-token"] = token
			},
		},
		{
			name: "missing token row", diagnosticField: "token.current-token.exists",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				delete(actual.tokens, "current-token")
			},
		},
		{
			name: "wrong staged token revoked state", diagnosticField: "token.staged-token.revoked",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				token := actual.tokens["staged-token"]
				token.RevokedAt = nonNilMariaDBFIX007Time()
				actual.tokens["staged-token"] = token
			},
		},
	}
}

func TestMariaDBFIX008SharedPolicyOracleCoreMutationMatrix(t *testing.T) {
	expected := mariaDBFIX008PolicyOracleFixture()
	if mismatches := compareMariaDBFIX008StrongOwnership(expected, cloneMariaDBFIX007OwnershipSemanticState(expected)); len(mismatches) != 0 {
		t.Fatalf("valid policy oracle fixture mismatched: %s", mariaDBFIX008FormatMismatches(mismatches))
	}
	for _, mutation := range mariaDBFIX008PolicyMutationMatrix() {
		mutation := mutation
		t.Run(mutation.name, func(t *testing.T) {
			actual := cloneMariaDBFIX007OwnershipSemanticState(expected)
			mutation.mutate(&actual)
			mismatches := compareMariaDBFIX008StrongOwnership(expected, actual)
			if !mariaDBFIX008HasMismatchField(mismatches, mutation.diagnosticField) {
				t.Fatalf("shared policy core did not report %q: %s", mutation.diagnosticField, mariaDBFIX008FormatMismatches(mismatches))
			}
		})
	}
}
