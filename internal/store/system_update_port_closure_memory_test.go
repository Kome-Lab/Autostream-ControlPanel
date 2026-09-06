package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMemorySTPortV2SelfUpdateUsesIndependentPolicyCounters(t *testing.T) {
	for _, wrongProjection := range []bool{false, true} {
		name := "source_11_projection_17"
		if wrongProjection {
			name = "host_source_is_not_projection"
		}
		t.Run(name, func(t *testing.T) {
			policies, registry, updates, params := readyMemoryHostSelfUpdate(t)
			policy := policies.policies["host-agent-a"]
			policy.Revision, policy.ProjectionRevision, policy.LocalExecutorPolicyRevision = 11, 17, 23
			policies.policies[policy.UpdaterID] = policy
			host := updates.executionHosts[params.ExecutionHostID]
			host.PolicyRevision = 17
			if wrongProjection {
				host.PolicyRevision = 11
			}
			updates.executionHosts[host.ExecutionHostID] = host
			created, fresh, err := updates.CreateSystemUpdateHostSelfUpdate(t.Context(), registry, policies, params)
			if wrongProjection {
				if !errors.Is(err, ErrSystemUpdateOwnershipConflict) || fresh || len(updates.hostSelfUpdates) != 0 {
					t.Fatalf("wrong projection accepted: %v", err)
				}
				return
			}
			if err != nil || !fresh || created.ExpectedSourcePolicyRevision != 11 || created.ExpectedProjectionRevision != 17 || created.ExpectedLocalExecutorPolicyRevision != 23 {
				t.Fatalf("independent counters rejected: %v", err)
			}
			issued, err := updates.IssueSystemUpdateHostSelfUpdateGrant(t.Context(), registry, policies, IssueSystemUpdateHostSelfUpdateGrantParams{SelfUpdateID: created.ID, ExecutionHostID: created.ExecutionHostID, AgentServiceID: created.AgentServiceID, ExpectedRevision: created.Revision, Operation: SystemUpdateHostSelfUpdateGrantStage, PlanSHA256: strings.Repeat("d", 64), SessionID: "independent-counters", Now: params.Now.Add(time.Second), TTL: time.Minute})
			if err != nil || !issued.Issued || issued.RawToken == "" || issued.Grant.ExpectedSourcePolicyRevision != 11 || issued.Grant.ExpectedProjectionRevision != 17 {
				t.Fatalf("real self-update grant rejected independent counters: %v", err)
			}
		})
	}
}
