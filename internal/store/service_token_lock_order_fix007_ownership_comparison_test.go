package store

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"
)

type mariaDBFIX007OwnershipSemanticSnapshot struct {
	hosts       map[string]SystemUpdateExecutionHost
	services    map[string]RegisteredService
	policies    map[string]UpdaterPolicy
	tokens      map[string]ServiceToken
	fixtureIDs  map[string]struct{}
	relevantIDs map[string]struct{}
}

type mariaDBFIX007OwnershipAction struct {
	fixture   mariaDBFIX005PullFixture
	operation string
	result    SystemUpdateExecutionHost
}

func snapshotMariaDBFIX007OwnershipSemanticState(
	t *testing.T,
	ctx context.Context,
	fixtures ...mariaDBFIX005PullFixture,
) mariaDBFIX007OwnershipSemanticSnapshot {
	t.Helper()
	if len(fixtures) == 0 {
		t.Fatal("ownership snapshot requires at least one fixture")
	}
	snapshot := mariaDBFIX007OwnershipSemanticSnapshot{
		hosts:       make(map[string]SystemUpdateExecutionHost),
		services:    make(map[string]RegisteredService),
		policies:    make(map[string]UpdaterPolicy),
		tokens:      make(map[string]ServiceToken),
		fixtureIDs:  make(map[string]struct{}),
		relevantIDs: make(map[string]struct{}),
	}
	auth := fixtures[0].auth
	policies := fixtures[0].policies
	services, err := auth.ListServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range services {
		snapshot.services[service.ServiceID] = service
	}
	policyRows, err := policies.ListUpdaterPolicies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, policy := range policyRows {
		snapshot.policies[policy.UpdaterID] = policy
	}
	tokens, err := auth.ListServiceTokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range tokens {
		snapshot.tokens[token.ID] = token
	}
	for _, fixture := range fixtures {
		host, err := fixture.updates.GetSystemUpdateExecutionHost(
			ctx,
			fixture.params.ExecutionHostID,
		)
		if err != nil && !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
		if err == nil {
			snapshot.hosts[host.ExecutionHostID] = host
		}
		for _, serviceID := range []string{
			fixture.peerID,
			fixture.targetID,
			fixture.params.ServiceID,
		} {
			snapshot.fixtureIDs[serviceID] = struct{}{}
			service, exists := snapshot.services[serviceID]
			if !exists {
				t.Fatalf("ownership fixture service %q is missing", serviceID)
			}
			for _, tokenID := range []string{
				service.TokenID,
				service.StagedNodePreviousTokenID,
				service.StagedNodeTokenID,
			} {
				if tokenID != "" {
					snapshot.relevantIDs[tokenID] = struct{}{}
				}
			}
		}
	}
	return snapshot
}

func cloneMariaDBFIX007OwnershipSemanticState(
	source mariaDBFIX007OwnershipSemanticSnapshot,
) mariaDBFIX007OwnershipSemanticSnapshot {
	clone := mariaDBFIX007OwnershipSemanticSnapshot{
		hosts:       make(map[string]SystemUpdateExecutionHost, len(source.hosts)),
		services:    make(map[string]RegisteredService, len(source.services)),
		policies:    make(map[string]UpdaterPolicy, len(source.policies)),
		tokens:      make(map[string]ServiceToken, len(source.tokens)),
		fixtureIDs:  make(map[string]struct{}, len(source.fixtureIDs)),
		relevantIDs: make(map[string]struct{}, len(source.relevantIDs)),
	}
	for id, host := range source.hosts {
		clone.hosts[id] = host
	}
	for id, service := range source.services {
		clone.services[id] = service
	}
	for id, policy := range source.policies {
		clone.policies[id] = policy
	}
	for id, token := range source.tokens {
		clone.tokens[id] = token
	}
	for id := range source.fixtureIDs {
		clone.fixtureIDs[id] = struct{}{}
	}
	for id := range source.relevantIDs {
		clone.relevantIDs[id] = struct{}{}
	}
	return clone
}

func assertMariaDBFIX007StrongOwnershipFinalState(
	t *testing.T,
	ctx context.Context,
	before mariaDBFIX007OwnershipSemanticSnapshot,
	actions []mariaDBFIX007OwnershipAction,
	tokenMutation string,
	mutationOutcome mariaDBServiceTokenMutationResult,
) {
	t.Helper()
	if len(actions) == 0 {
		t.Fatal("strong ownership oracle requires at least one action")
	}
	expected := cloneMariaDBFIX007OwnershipSemanticState(before)
	fixtures := make([]mariaDBFIX005PullFixture, 0, len(actions))
	for _, action := range actions {
		fixtures = append(fixtures, action.fixture)
		hostID := action.fixture.params.ExecutionHostID
		host, exists := expected.hosts[hostID]
		if !exists {
			host = syntheticSystemUpdateExecutionHost(hostID)
		}
		pullPolicy, exists := expected.policies[action.fixture.params.ServiceID]
		if !exists {
			t.Fatalf("pull policy %q is missing", action.fixture.params.ServiceID)
		}
		pullService := expected.services[action.fixture.params.ServiceID]
		switch action.operation {
		case "activate":
			host.TransportMode = SystemUpdateTransportPullV2
			host.AgentServiceID = action.fixture.params.ServiceID
			host.OwnershipEpoch++
			host.PolicyRevision = pullPolicy.ProjectionRevision
			pullService.OwnershipEpoch = host.OwnershipEpoch
		case "deactivate":
			host.TransportMode = SystemUpdateTransportPullV2
			host.AgentServiceID = action.fixture.params.ServiceID
			host.OwnershipEpoch++
			host.PolicyRevision = pullPolicy.ProjectionRevision
			pullService.OwnershipEpoch = 0
		default:
			t.Fatalf("unknown ownership operation %q", action.operation)
		}
		expected.hosts[hostID] = host
		expected.services[pullService.ServiceID] = pullService
		assertMariaDBFIX007OwnershipResultMatches(t, action, host)
	}

	if tokenMutation != "" {
		oldTokenID := actions[0].fixture.agentToken.ID
		expected.relevantIDs[oldTokenID] = struct{}{}
		oldToken, exists := expected.tokens[oldTokenID]
		if !exists {
			t.Fatalf("mutated token %q is missing from ownership pre-state", oldTokenID)
		}
		switch tokenMutation {
		case "rotate":
			if mutationOutcome.err != nil || mutationOutcome.token.ID == "" {
				t.Fatalf("ownership/rotate result = %s, want success", formatSafeSensitiveCompositeDiagnostic(mutationOutcome))
			}
			oldToken.RevokedAt = nonNilMariaDBFIX007Time()
			expected.tokens[oldTokenID] = oldToken
			replacement := mutationOutcome.token
			replacement.RevokedAt = nil
			expected.tokens[replacement.ID] = replacement
			expected.relevantIDs[replacement.ID] = struct{}{}
			for serviceID, service := range expected.services {
				if service.TokenID != oldTokenID {
					continue
				}
				service.TokenID = replacement.ID
				service.LastHeartbeatAt = nil
				service.ReportedCapabilities = map[string]any{}
				service.StagedNodePreviousTokenID = ""
				service.StagedNodeTokenID = ""
				service.StagedNodeTokenHash = ""
				service.StagedNodeTokenScopes = nil
				service.StagedNodeTokenCiphertext = ""
				service.StagedNodeTokenNonce = ""
				service.StagedNodeActivationTokenHash = ""
				service.StagedNodeTokenAt = nil
				expected.services[serviceID] = service
			}
		case "revoke":
			if mutationOutcome.err != nil || mutationOutcome.token.ID != "" {
				t.Fatalf("ownership/revoke result = %s, want success", formatSafeSensitiveCompositeDiagnostic(mutationOutcome))
			}
			oldToken.RevokedAt = nonNilMariaDBFIX007Time()
			expected.tokens[oldTokenID] = oldToken
			for serviceID, service := range expected.services {
				if service.TokenID != oldTokenID {
					continue
				}
				service.LastHeartbeatAt = nil
				service.ReportedCapabilities = map[string]any{}
				expected.services[serviceID] = service
			}
		default:
			t.Fatalf("unknown token mutation %q", tokenMutation)
		}
	}

	after := snapshotMariaDBFIX007OwnershipSemanticState(t, ctx, fixtures...)
	if mismatches := compareMariaDBFIX008StrongOwnership(expected, after); len(mismatches) != 0 {
		t.Fatalf("strong ownership final-state mismatch: %s", mariaDBFIX008FormatMismatches(mismatches))
	}
}

func assertMariaDBFIX007OwnershipResultMatches(
	t *testing.T,
	action mariaDBFIX007OwnershipAction,
	want SystemUpdateExecutionHost,
) {
	t.Helper()
	if !mariaDBFIX007EqualOwnershipHost(action.result, want) {
		t.Fatalf("%s ownership result = %#v, want %#v", action.operation, action.result, want)
	}
}

func mariaDBFIX007EqualOwnershipHost(a, b SystemUpdateExecutionHost) bool {
	return a.ExecutionHostID == b.ExecutionHostID &&
		a.TransportMode == b.TransportMode &&
		a.AgentServiceID == b.AgentServiceID &&
		a.OwnershipEpoch == b.OwnershipEpoch &&
		a.PolicyRevision == b.PolicyRevision
}

func compareMariaDBFIX008StrongOwnership(
	expected, actual mariaDBFIX007OwnershipSemanticSnapshot,
) []mariaDBFIX008OracleMismatch {
	mismatches := make([]mariaDBFIX008OracleMismatch, 0)
	hostIDs := make([]string, 0, len(expected.hosts))
	for hostID := range expected.hosts {
		hostIDs = append(hostIDs, hostID)
	}
	sort.Strings(hostIDs)
	for _, hostID := range hostIDs {
		want := expected.hosts[hostID]
		got, exists := actual.hosts[hostID]
		prefix := "host." + hostID + "."
		if !exists {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"exists", "missing; want present"))
			continue
		}
		if got.ExecutionHostID != want.ExecutionHostID {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"execution_host_id", "got %q want %q", got.ExecutionHostID, want.ExecutionHostID))
		}
		if got.TransportMode != want.TransportMode {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"transport_mode", "got %q want %q", got.TransportMode, want.TransportMode))
		}
		if got.AgentServiceID != want.AgentServiceID {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"agent_service_id", "got %q want %q", got.AgentServiceID, want.AgentServiceID))
		}
		if got.OwnershipEpoch != want.OwnershipEpoch {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"ownership_epoch", "got %d want %d", got.OwnershipEpoch, want.OwnershipEpoch))
		}
		if got.PolicyRevision != want.PolicyRevision {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"policy_revision", "got %d want %d", got.PolicyRevision, want.PolicyRevision))
		}
		wantActive := mariaDBFIX008ActivePolicyIDs(want, expected)
		gotActive := mariaDBFIX008ActivePolicyIDs(got, actual)
		if !reflect.DeepEqual(gotActive, wantActive) {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"active_policy_binding", "got active %v agent %q want active %v agent %q", gotActive, got.AgentServiceID, wantActive, want.AgentServiceID))
		}
		policy, policyExists := actual.policies[got.AgentServiceID]
		if !policyExists || policy.ProjectionRevision != got.PolicyRevision {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"policy_revision_closure", "agent policy exists=%t projection=%d host revision=%d", policyExists, policy.ProjectionRevision, got.PolicyRevision))
		}
	}

	serviceIDs := mariaDBFIX008SortedSetKeys(expected.fixtureIDs)
	for _, serviceID := range serviceIDs {
		want, wantExists := expected.services[serviceID]
		got, gotExists := actual.services[serviceID]
		prefix := "service." + serviceID + "."
		if wantExists != gotExists {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"exists", "got %t want %t", gotExists, wantExists))
			continue
		}
		if !wantExists {
			continue
		}
		if got.ServiceID != want.ServiceID {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"service_id", "got %q want %q", got.ServiceID, want.ServiceID))
		}
		if got.ServiceType != want.ServiceType {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"service_type", "got %q want %q", got.ServiceType, want.ServiceType))
		}
		if got.ExecutionHostID != want.ExecutionHostID {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"execution_host_id", "got %q want %q", got.ExecutionHostID, want.ExecutionHostID))
		}
		if got.TransportMode != want.TransportMode {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"transport_mode", "got %q want %q", got.TransportMode, want.TransportMode))
		}
		if got.OwnershipEpoch != want.OwnershipEpoch {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"ownership_epoch", "got %d want %d", got.OwnershipEpoch, want.OwnershipEpoch))
		}
		if got.TokenID != want.TokenID {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"current_token_id", "got %q want %q", got.TokenID, want.TokenID))
		}
		if got.StagedNodePreviousTokenID != want.StagedNodePreviousTokenID {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"staged_previous_token_id", "got %q want %q", got.StagedNodePreviousTokenID, want.StagedNodePreviousTokenID))
		}
		if got.StagedNodeTokenID != want.StagedNodeTokenID {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"staged_token_id", "got %q want %q", got.StagedNodeTokenID, want.StagedNodeTokenID))
		}
	}

	for _, policyID := range serviceIDs {
		want, wantExists := expected.policies[policyID]
		got, gotExists := actual.policies[policyID]
		prefix := "policy." + policyID + "."
		if wantExists != gotExists {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"exists", "got %t want %t", gotExists, wantExists))
			continue
		}
		if !wantExists {
			continue
		}
		if got.UpdaterID != want.UpdaterID {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"updater_id", "got %q want %q", got.UpdaterID, want.UpdaterID))
		}
		if got.Revision != want.Revision {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"revision", "got %d want %d", got.Revision, want.Revision))
		}
		if got.ProjectionRevision != want.ProjectionRevision {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"projection_revision", "got %d want %d", got.ProjectionRevision, want.ProjectionRevision))
		}
		if got.LocalExecutorPolicyRevision != want.LocalExecutorPolicyRevision {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"local_executor_policy_revision", "got %d want %d", got.LocalExecutorPolicyRevision, want.LocalExecutorPolicyRevision))
		}
		if got.TransportMode != want.TransportMode {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"transport_mode", "got %q want %q", got.TransportMode, want.TransportMode))
		}
		if got.ExecutionHostID != want.ExecutionHostID {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"execution_host_id", "got %q want %q", got.ExecutionHostID, want.ExecutionHostID))
		}
		if got.LocalExecutorPolicySHA256 != want.LocalExecutorPolicySHA256 {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"local_executor_policy_sha256", "got %q want %q", got.LocalExecutorPolicySHA256, want.LocalExecutorPolicySHA256))
		}
	}

	tokenIDs := make(map[string]struct{}, len(expected.relevantIDs))
	for tokenID := range expected.relevantIDs {
		tokenIDs[tokenID] = struct{}{}
	}
	for _, snapshot := range []mariaDBFIX007OwnershipSemanticSnapshot{expected, actual} {
		for _, serviceID := range serviceIDs {
			service, exists := snapshot.services[serviceID]
			if !exists {
				continue
			}
			for _, tokenID := range []string{service.TokenID, service.StagedNodePreviousTokenID, service.StagedNodeTokenID} {
				if tokenID != "" {
					tokenIDs[tokenID] = struct{}{}
				}
			}
		}
	}
	for _, tokenID := range mariaDBFIX008SortedSetKeys(tokenIDs) {
		want, wantExists := expected.tokens[tokenID]
		got, gotExists := actual.tokens[tokenID]
		prefix := "token." + tokenID + "."
		if wantExists != gotExists {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"exists", "got %t want %t", gotExists, wantExists))
		}
		if wantExists && gotExists {
			if got.ID != want.ID {
				mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"id", "got %q want %q", got.ID, want.ID))
			}
			if got.ServiceType != want.ServiceType {
				mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"service_type", "got %q want %q", got.ServiceType, want.ServiceType))
			}
			if (got.RevokedAt != nil) != (want.RevokedAt != nil) {
				mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"revoked", "got %t want %t", got.RevokedAt != nil, want.RevokedAt != nil))
			}
		}
		wantReferences := mariaDBFIX007TokenReferenceOwners(expected.services, tokenID)
		gotReferences := mariaDBFIX007TokenReferenceOwners(actual.services, tokenID)
		if !reflect.DeepEqual(gotReferences, wantReferences) {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"references", "got %v want %v", gotReferences, wantReferences))
		}
	}
	return mismatches
}

func mariaDBFIX008ActivePolicyIDs(
	host SystemUpdateExecutionHost,
	snapshot mariaDBFIX007OwnershipSemanticSnapshot,
) []string {
	active := make([]string, 0, 2)
	for policyID, policy := range snapshot.policies {
		service, serviceExists := snapshot.services[policyID]
		if host.TransportMode == SystemUpdateTransportPullV2 &&
			policy.TransportMode == SystemUpdateTransportPullV2 &&
			policy.ExecutionHostID == host.ExecutionHostID &&
			serviceExists && service.TransportMode == SystemUpdateTransportPullV2 &&
			service.ExecutionHostID == host.ExecutionHostID &&
			service.OwnershipEpoch == host.OwnershipEpoch {
			active = append(active, policyID)
		}
	}
	sort.Strings(active)
	return active
}

func mariaDBFIX008SortedSetKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func nonNilMariaDBFIX007Time() *time.Time {
	value := time.Unix(1, 0).UTC()
	return &value
}
