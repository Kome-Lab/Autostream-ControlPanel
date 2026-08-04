package store

import (
	"errors"
	"testing"
	"time"
)

func TestMemorySystemUpdateStoreActivePullRecoveryRequiresExactTargetAndOwnership(t *testing.T) {
	type fixture struct {
		updates *MemorySystemUpdateStore
		job     SystemUpdateJob
	}
	setup := func(t *testing.T) fixture {
		t.Helper()
		updates := NewMemorySystemUpdateStore()
		if _, err := updates.SwitchSystemUpdateExecutionHost(
			t.Context(),
			"host-a",
			0,
			SystemUpdateTransportPullV2,
			"host-agent-a",
			7,
		); err != nil {
			t.Fatal(err)
		}
		job, created, err := updates.CreateSystemUpdateJob(t.Context(), CreateSystemUpdateJobParams{
			TargetID: "worker-a", TargetServiceType: "worker",
			AgentServiceID: "host-agent-a", ExecutionHostID: "host-a", DeploymentMode: "systemd",
			CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0",
			Strategy: SystemUpdateStrategyWhenIdle, IdempotencyKey: "active-pull-recovery-fence",
			RequestedByUserID: "admin",
		})
		if err != nil || !created {
			t.Fatalf("create active pull job: created=%v err=%v", created, err)
		}
		claim, clear, err := updates.ClaimSystemUpdateJob(
			t.Context(),
			"host-agent-a",
			"host-a",
			"",
			map[string]string{"worker-a": "systemd"},
			time.Now().UTC(),
			time.Minute,
		)
		if err != nil || clear || claim.Job.ID != job.ID {
			t.Fatalf("initial active pull claim = %#v clear=%v err=%v", claim, clear, err)
		}
		return fixture{updates: updates, job: job}
	}

	t.Run("exact recovery", func(t *testing.T) {
		fixture := setup(t)
		claim, clear, err := fixture.updates.ClaimSystemUpdateJob(
			t.Context(),
			"host-agent-a",
			"host-a",
			fixture.job.ID,
			map[string]string{"worker-a": "systemd"},
			time.Now().UTC(),
			time.Minute,
		)
		if err != nil || clear || claim.Job.ID != fixture.job.ID || !claim.RecoveryRequired || claim.Job.Status != SystemUpdateStatusReconciling {
			t.Fatalf("exact active pull recovery = %#v clear=%v err=%v", claim, clear, err)
		}
	})

	for _, test := range []struct {
		name            string
		agentServiceID  string
		executionHostID string
		eligibleTargets map[string]string
		mutateOwnership func(*SystemUpdateExecutionHost)
		wantErr         error
	}{
		{
			name: "foreign agent", agentServiceID: "foreign-agent", executionHostID: "host-a",
			eligibleTargets: map[string]string{"worker-a": "systemd"}, wantErr: ErrSystemUpdateOwnershipConflict,
		},
		{
			name: "wrong host", agentServiceID: "host-agent-a", executionHostID: "host-b",
			eligibleTargets: map[string]string{"worker-a": "systemd"}, wantErr: ErrSystemUpdateActiveUnavailable,
		},
		{
			name: "missing target", agentServiceID: "host-agent-a", executionHostID: "host-a",
			eligibleTargets: map[string]string{}, wantErr: ErrSystemUpdateActiveUnavailable,
		},
		{
			name: "deployment target drift", agentServiceID: "host-agent-a", executionHostID: "host-a",
			eligibleTargets: map[string]string{"worker-a": "docker"}, wantErr: ErrSystemUpdateActiveUnavailable,
		},
		{
			name: "ownership epoch drift", agentServiceID: "host-agent-a", executionHostID: "host-a",
			eligibleTargets: map[string]string{"worker-a": "systemd"},
			mutateOwnership: func(ownership *SystemUpdateExecutionHost) { ownership.OwnershipEpoch++ },
			wantErr:         ErrSystemUpdateOwnershipConflict,
		},
		{
			name: "policy revision drift", agentServiceID: "host-agent-a", executionHostID: "host-a",
			eligibleTargets: map[string]string{"worker-a": "systemd"},
			mutateOwnership: func(ownership *SystemUpdateExecutionHost) { ownership.PolicyRevision++ },
			wantErr:         ErrSystemUpdateOwnershipConflict,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := setup(t)
			if test.mutateOwnership != nil {
				fixture.updates.mu.Lock()
				ownership := fixture.updates.executionHosts["host-a"]
				test.mutateOwnership(&ownership)
				fixture.updates.executionHosts["host-a"] = ownership
				fixture.updates.mu.Unlock()
			}
			claim, clear, err := fixture.updates.ClaimSystemUpdateJob(
				t.Context(),
				test.agentServiceID,
				test.executionHostID,
				fixture.job.ID,
				test.eligibleTargets,
				time.Now().UTC(),
				time.Minute,
			)
			if !errors.Is(err, test.wantErr) || clear || claim.Job.ID != "" {
				t.Fatalf("drifted active pull recovery = %#v clear=%v err=%v, want %v", claim, clear, err, test.wantErr)
			}
		})
	}
}
