package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

func TestMemorySystemUpdateMutationGrantPortBindingUsesRuntimeHashAndExactJobSnapshot(t *testing.T) {
	base := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	updates, job, leaseToken := preparePortMutationGrantJob(t, base, SystemUpdateStatusInstalling)
	binding := validPortMutationGrantBinding(t, job)

	if binding.PlanSHA256 == job.PortReconfigure.PortPlanSHA256 {
		t.Fatal("runtime executor hash must remain distinct from the stable stored intent hash")
	}
	issued, err := updates.IssueSystemUpdateMutationGrant(t.Context(), job.ID, IssueSystemUpdateMutationGrantParams{
		AgentServiceID: "host-agent-a", ExecutionHostID: job.ExecutionHostID,
		LeaseToken: leaseToken, LeaseGeneration: job.LeaseGeneration, Binding: binding,
	}, base.Add(time.Second), time.Minute)
	if err != nil {
		t.Fatalf("issue exact port grant: %v", err)
	}
	if issued.Grant.Binding.TargetServiceType != job.TargetServiceType ||
		issued.Grant.Binding.JobOperation != SystemUpdateOperationPortReconfigure ||
		issued.Grant.Binding.PortReconfigure == nil ||
		issued.Grant.Binding.PortReconfigure.PortPlanSHA256 != binding.PlanSHA256 {
		t.Fatalf("issued port binding = %#v", issued.Grant.Binding)
	}
	tamperedConsume := clonePortMutationGrantBinding(binding)
	tamperedConsume.PortReconfigure.NewPort++
	tamperedHash := portMutationGrantRuntimeHashForTest(t, job.ID, job.LeaseGeneration, tamperedConsume)
	tamperedConsume.PlanSHA256 = tamperedHash
	tamperedConsume.PortReconfigure.PortPlanSHA256 = tamperedHash
	if _, _, err := updates.ConsumeSystemUpdateMutationGrant(
		t.Context(), job.ID, issued.GrantToken, job.LeaseGeneration, tamperedConsume, base.Add(2*time.Second),
	); !errors.Is(err, ErrSystemUpdateMutationGrantConflict) {
		t.Fatalf("consume with internally consistent but changed port plan = %v", err)
	}
	if _, replayed, err := updates.ConsumeSystemUpdateMutationGrant(
		t.Context(), job.ID, issued.GrantToken, job.LeaseGeneration, binding, base.Add(2*time.Second),
	); err != nil || replayed {
		t.Fatalf("consume exact port grant replayed=%v err=%v", replayed, err)
	}

	arbitraryRuntimeHash := clonePortMutationGrantBinding(binding)
	arbitraryRuntimeHash.PlanSHA256 = strings.Repeat("f", 64)
	arbitraryRuntimeHash.PortReconfigure.PortPlanSHA256 = arbitraryRuntimeHash.PlanSHA256
	if _, err := updates.IssueSystemUpdateMutationGrant(t.Context(), job.ID, IssueSystemUpdateMutationGrantParams{
		AgentServiceID: "host-agent-a", ExecutionHostID: job.ExecutionHostID,
		LeaseToken: leaseToken, LeaseGeneration: job.LeaseGeneration, Binding: arbitraryRuntimeHash,
	}, base.Add(3*time.Second), time.Minute); !errors.Is(err, ErrSystemUpdateAuthorizationMismatch) {
		t.Fatalf("issue with arbitrary runtime hash = %v, want authorization mismatch", err)
	}

	tests := []struct {
		name   string
		mutate func(*SystemUpdateMutationGrantBinding)
	}{
		{name: "target id", mutate: func(value *SystemUpdateMutationGrantBinding) {
			value.TargetID = "worker-02"
		}},
		{name: "service type", mutate: func(value *SystemUpdateMutationGrantBinding) {
			value.TargetServiceType = "observability"
		}},
		{name: "job operation", mutate: func(value *SystemUpdateMutationGrantBinding) {
			value.JobOperation = SystemUpdateOperationSoftwareUpdate
			value.Operation = SystemUpdateMutationOperationApply
			value.PortReconfigure = nil
		}},
		{name: "previous port", mutate: func(value *SystemUpdateMutationGrantBinding) {
			value.PortReconfigure.OldPort++
		}},
		{name: "new port", mutate: func(value *SystemUpdateMutationGrantBinding) {
			value.PortReconfigure.NewPort++
		}},
		{name: "endpoint revision", mutate: func(value *SystemUpdateMutationGrantBinding) {
			value.PortReconfigure.ExpectedEndpointRevision++
			value.PortReconfigure.TargetEndpointRevision++
		}},
		{name: "config revision", mutate: func(value *SystemUpdateMutationGrantBinding) {
			value.PortReconfigure.ExpectedConfigRevision++
			value.PortReconfigure.TargetConfigRevision++
		}},
		{name: "config digest", mutate: func(value *SystemUpdateMutationGrantBinding) {
			value.PortReconfigure.ExpectedConfigSHA256 = "sha256:" + strings.Repeat("d", 64)
		}},
		{name: "target config digest", mutate: func(value *SystemUpdateMutationGrantBinding) {
			value.PortReconfigure.TargetConfigSHA256 = "sha256:" + strings.Repeat("e", 64)
		}},
		{name: "source policy revision", mutate: func(value *SystemUpdateMutationGrantBinding) {
			value.PortReconfigure.ExpectedSourcePolicyRevision++
		}},
		{name: "projection revision", mutate: func(value *SystemUpdateMutationGrantBinding) {
			value.PortReconfigure.ExpectedUpdaterPolicyRevision++
		}},
		{name: "executor policy revision", mutate: func(value *SystemUpdateMutationGrantBinding) {
			value.PortReconfigure.ExpectedExecutorPolicyRevision++
		}},
		{name: "executor policy digest", mutate: func(value *SystemUpdateMutationGrantBinding) {
			value.PortReconfigure.ExpectedExecutorPolicySHA256 = "sha256:" + strings.Repeat("f", 64)
		}},
		{name: "ownership epoch", mutate: func(value *SystemUpdateMutationGrantBinding) {
			value.OwnershipEpoch++
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := clonePortMutationGrantBinding(binding)
			test.mutate(&changed)
			if changed.PortReconfigure != nil {
				runtimeHash := portMutationGrantRuntimeHashForTest(t, job.ID, job.LeaseGeneration, changed)
				changed.PlanSHA256 = runtimeHash
				changed.PortReconfigure.PortPlanSHA256 = runtimeHash
			}
			_, err := updates.IssueSystemUpdateMutationGrant(t.Context(), job.ID, IssueSystemUpdateMutationGrantParams{
				AgentServiceID: "host-agent-a", ExecutionHostID: job.ExecutionHostID,
				LeaseToken: leaseToken, LeaseGeneration: job.LeaseGeneration, Binding: changed,
			}, base.Add(3*time.Second), time.Minute)
			if !errors.Is(err, ErrSystemUpdateAuthorizationMismatch) {
				t.Fatalf("issue with changed %s = %v, want authorization mismatch", test.name, err)
			}
		})
	}
}

func TestMemorySystemUpdateMutationGrantRejectsSoftwarePortUnionMixing(t *testing.T) {
	base := time.Date(2026, 7, 28, 13, 0, 0, 0, time.UTC)
	softwareUpdates, softwareJob, softwareClaim := prepareMutationGrantInstallingJob(t, base, time.Minute)
	softwareBinding := validMutationGrantBinding()
	softwareBinding.JobOperation = SystemUpdateOperationSoftwareUpdate
	softwareBinding.TargetServiceType = softwareJob.TargetServiceType
	softwareBinding.PortReconfigure = &SystemUpdatePortReconfiguration{
		Result: SystemUpdatePortReconfigurationApplied,
	}
	if _, err := softwareUpdates.IssueSystemUpdateMutationGrant(
		t.Context(), softwareJob.ID,
		IssueSystemUpdateMutationGrantParams{
			AgentServiceID: "updater-central", LeaseToken: softwareClaim.LeaseToken,
			LeaseGeneration: softwareClaim.LeaseGeneration, Binding: softwareBinding,
		},
		base.Add(2*time.Second), time.Minute,
	); !errors.Is(err, ErrInvalidSystemUpdate) {
		t.Fatalf("software job with port binding = %v", err)
	}

	portUpdates, portJob, leaseToken := preparePortMutationGrantJob(t, base.Add(time.Hour), SystemUpdateStatusInstalling)
	legacySoftwareBinding := SystemUpdateMutationGrantBinding{
		HostID: portJob.ExecutionHostID, TransportMode: SystemUpdateTransportPullV2,
		OwnershipEpoch: portJob.OwnershipEpoch, PolicyRevision: portJob.PolicyRevision,
		TargetID: portJob.TargetID, TargetVersion: portJob.TargetVersion,
		DeploymentMode: portJob.DeploymentMode, JobOperation: SystemUpdateOperationSoftwareUpdate,
		Operation: SystemUpdateMutationOperationApply, PlanSHA256: strings.Repeat("a", 64),
		SessionID: "session-port-wrong-union",
	}
	if _, err := portUpdates.IssueSystemUpdateMutationGrant(
		t.Context(), portJob.ID,
		IssueSystemUpdateMutationGrantParams{
			AgentServiceID: "host-agent-a", ExecutionHostID: portJob.ExecutionHostID,
			LeaseToken: leaseToken, LeaseGeneration: portJob.LeaseGeneration, Binding: legacySoftwareBinding,
		},
		base.Add(time.Hour+time.Second), time.Minute,
	); !errors.Is(err, ErrSystemUpdateAuthorizationMismatch) {
		t.Fatalf("port job with software binding = %v", err)
	}
}

func TestMemorySystemUpdateMutationGrantPortOperationMatchesState(t *testing.T) {
	base := time.Date(2026, 7, 28, 14, 0, 0, 0, time.UTC)
	updates, job, leaseToken := preparePortMutationGrantJob(t, base, SystemUpdateStatusReconciling)
	binding := validPortMutationGrantBinding(t, job)
	if _, err := updates.IssueSystemUpdateMutationGrant(t.Context(), job.ID, IssueSystemUpdateMutationGrantParams{
		AgentServiceID: "host-agent-a", ExecutionHostID: job.ExecutionHostID,
		LeaseToken: leaseToken, LeaseGeneration: job.LeaseGeneration, Binding: binding,
	}, base.Add(time.Second), time.Minute); !errors.Is(err, ErrSystemUpdateAuthorizationState) {
		t.Fatalf("port apply grant in reconciling state = %v", err)
	}

	binding.Operation = SystemUpdateMutationOperationPortReconfigureReconcile
	issued, err := updates.IssueSystemUpdateMutationGrant(t.Context(), job.ID, IssueSystemUpdateMutationGrantParams{
		AgentServiceID: "host-agent-a", ExecutionHostID: job.ExecutionHostID,
		LeaseToken: leaseToken, LeaseGeneration: job.LeaseGeneration, Binding: binding,
	}, base.Add(time.Second), time.Minute)
	if err != nil {
		t.Fatalf("port reconcile grant in reconciling state: %v", err)
	}
	if _, replayed, err := updates.ConsumeSystemUpdateMutationGrant(
		t.Context(), job.ID, issued.GrantToken, job.LeaseGeneration, binding, base.Add(2*time.Second),
	); err != nil || replayed {
		t.Fatalf("consume port reconcile grant replayed=%v err=%v", replayed, err)
	}
}

func preparePortMutationGrantJob(
	t *testing.T,
	base time.Time,
	status string,
) (*MemorySystemUpdateStore, SystemUpdateJob, string) {
	t.Helper()
	const leaseToken = "lease-token-port-mutation-grant-0000000000000001"
	leaseExpiry := base.Add(5 * time.Minute)
	job := SystemUpdateJob{
		ID: "job-port-mutation-0001", TargetID: "worker-01", TargetServiceType: "worker",
		Operation: SystemUpdateOperationPortReconfigure,
		PortReconfigure: &SystemUpdatePortReconfiguration{
			NetworkNamespace: "host", Protocol: SystemUpdatePortProtocolTCP,
			OldPort: 8081, NewPort: 18081,
			ExpectedEndpointRevision: 4, TargetEndpointRevision: 5,
			ExpectedConfigRevision: 7, TargetConfigRevision: 8,
			ExpectedConfigSHA256:           "sha256:" + strings.Repeat("a", 64),
			TargetConfigSHA256:             "sha256:" + strings.Repeat("b", 64),
			ExpectedSourcePolicyRevision:   11,
			ExpectedUpdaterPolicyRevision:  13,
			ExpectedExecutorPolicyRevision: 17,
			ExpectedExecutorPolicySHA256:   "sha256:" + strings.Repeat("c", 64),
		},
		DeploymentMode: "systemd", CurrentVersion: "v1.2.3", TargetVersion: "v1.2.3",
		Strategy: SystemUpdateStrategyMaintenance, Status: status,
		IdempotencyKey: "port-mutation-grant", RequestedByUserID: "user-01",
		AgentServiceID: "host-agent-a", ExecutionHostID: "host-a",
		TransportMode: SystemUpdateTransportPullV2, OwnershipEpoch: 3, PolicyRevision: 13,
		LeaseGeneration: 2, LeaseExpiresAt: &leaseExpiry, Sequence: 2, Progress: 70,
		CreatedAt: base.Add(-time.Minute), UpdatedAt: base,
		leaseTokenHash: security.HashToken(leaseToken),
	}
	intentHash, err := ComputeSystemUpdatePortIntentSHA256(job)
	if err != nil {
		t.Fatal(err)
	}
	job.PortReconfigure.PortPlanSHA256 = intentHash
	updates := NewMemorySystemUpdateStore()
	updates.jobs[job.ID] = job
	updates.executionHosts[job.ExecutionHostID] = SystemUpdateExecutionHost{
		ExecutionHostID: job.ExecutionHostID, TransportMode: job.TransportMode,
		AgentServiceID: job.AgentServiceID, OwnershipEpoch: job.OwnershipEpoch,
		PolicyRevision: job.PolicyRevision,
	}
	return updates, job, leaseToken
}

func validPortMutationGrantBinding(t *testing.T, job SystemUpdateJob) SystemUpdateMutationGrantBinding {
	t.Helper()
	binding := SystemUpdateMutationGrantBinding{
		HostID: job.ExecutionHostID, TransportMode: job.TransportMode,
		OwnershipEpoch: job.OwnershipEpoch, PolicyRevision: job.PolicyRevision,
		TargetID: job.TargetID, TargetServiceType: job.TargetServiceType,
		TargetVersion: job.TargetVersion, DeploymentMode: job.DeploymentMode,
		JobOperation:    SystemUpdateOperationPortReconfigure,
		Operation:       SystemUpdateMutationOperationPortReconfigure,
		SessionID:       "session-port-grant-0001",
		PortReconfigure: cloneSystemUpdatePortReconfiguration(job.PortReconfigure),
	}
	runtimeHash := portMutationGrantRuntimeHashForTest(t, job.ID, job.LeaseGeneration, binding)
	binding.PlanSHA256 = runtimeHash
	binding.PortReconfigure.PortPlanSHA256 = runtimeHash
	return binding
}

func clonePortMutationGrantBinding(binding SystemUpdateMutationGrantBinding) SystemUpdateMutationGrantBinding {
	copy := binding
	copy.PortReconfigure = cloneSystemUpdatePortReconfiguration(binding.PortReconfigure)
	return copy
}

func portMutationGrantRuntimeHashForTest(
	t *testing.T,
	jobID string,
	leaseGeneration int64,
	binding SystemUpdateMutationGrantBinding,
) string {
	t.Helper()
	if binding.PortReconfigure == nil {
		t.Fatal("port binding is required")
	}
	port := binding.PortReconfigure
	payload := struct {
		SchemaVersion                  int    `json:"schema_version"`
		JobID                          string `json:"job_id"`
		HostID                         string `json:"host_id"`
		TargetID                       string `json:"target_id"`
		ServiceType                    string `json:"service_type"`
		NetworkNamespace               string `json:"network_namespace"`
		Protocol                       string `json:"protocol"`
		OldPort                        int    `json:"old_port"`
		NewPort                        int    `json:"new_port"`
		ExpectedEndpointRevision       int64  `json:"expected_endpoint_revision"`
		TargetEndpointRevision         int64  `json:"target_endpoint_revision"`
		ExpectedConfigRevision         int64  `json:"expected_config_revision"`
		TargetConfigRevision           int64  `json:"target_config_revision"`
		ExpectedConfigSHA256           string `json:"expected_config_sha256"`
		TargetConfigSHA256             string `json:"target_config_sha256"`
		ExpectedSourcePolicyRevision   int64  `json:"expected_source_policy_revision"`
		ExpectedUpdaterPolicyRevision  int64  `json:"expected_updater_policy_revision"`
		ExpectedExecutorPolicyRevision int64  `json:"expected_executor_policy_revision"`
		ExpectedExecutorPolicySHA256   string `json:"expected_executor_policy_sha256"`
		OwnershipEpoch                 int64  `json:"ownership_epoch"`
		LeaseGeneration                uint64 `json:"lease_generation"`
		SessionID                      string `json:"session_id"`
	}{
		SchemaVersion: 1, JobID: jobID, HostID: binding.HostID,
		TargetID: binding.TargetID, ServiceType: binding.TargetServiceType,
		NetworkNamespace: port.NetworkNamespace, Protocol: string(port.Protocol),
		OldPort: port.OldPort, NewPort: port.NewPort,
		ExpectedEndpointRevision:       port.ExpectedEndpointRevision,
		TargetEndpointRevision:         port.TargetEndpointRevision,
		ExpectedConfigRevision:         port.ExpectedConfigRevision,
		TargetConfigRevision:           port.TargetConfigRevision,
		ExpectedConfigSHA256:           port.ExpectedConfigSHA256,
		TargetConfigSHA256:             port.TargetConfigSHA256,
		ExpectedSourcePolicyRevision:   port.ExpectedSourcePolicyRevision,
		ExpectedUpdaterPolicyRevision:  port.ExpectedUpdaterPolicyRevision,
		ExpectedExecutorPolicyRevision: port.ExpectedExecutorPolicyRevision,
		ExpectedExecutorPolicySHA256:   port.ExpectedExecutorPolicySHA256,
		OwnershipEpoch:                 binding.OwnershipEpoch,
		LeaseGeneration:                uint64(leaseGeneration),
		SessionID:                      binding.SessionID,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
