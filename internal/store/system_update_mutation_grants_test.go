package store

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

func TestMemorySystemUpdateMutationGrantIssueConsumeAndReplay(t *testing.T) {
	base := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	updates, job, claim := prepareMutationGrantInstallingJob(t, base, 45*time.Second)
	binding := validMutationGrantBinding()
	issued, err := updates.IssueSystemUpdateMutationGrant(t.Context(), job.ID, IssueSystemUpdateMutationGrantParams{
		AgentServiceID: "updater-central", LeaseToken: claim.LeaseToken,
		LeaseGeneration: claim.LeaseGeneration, Binding: binding,
	}, base.Add(2*time.Second), 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(issued.GrantToken, "ast_mutation_") || issued.Grant.ID == "" {
		t.Fatalf("issued grant = %#v", issued)
	}
	// The requested TTL is capped both at 90 seconds and at the current lease.
	wantExpiry := base.Add(time.Second).Add(45 * time.Second)
	if !issued.Grant.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("grant expiry = %s, want lease cap %s", issued.Grant.ExpiresAt, wantExpiry)
	}
	stored, ok := updates.mutationGrants[security.HashToken(issued.GrantToken)]
	if !ok || stored.tokenHash == "" || stored.tokenHash == issued.GrantToken {
		t.Fatalf("grant was not stored by hash only: %#v", stored)
	}
	if issued.Grant.tokenHash != "" {
		t.Fatal("public grant leaked its token hash")
	}

	wrong := binding
	wrong.SessionID = "session-wrong-0002"
	if _, _, err := updates.ConsumeSystemUpdateMutationGrant(t.Context(), job.ID, issued.GrantToken, claim.LeaseGeneration, wrong, base.Add(3*time.Second)); !errors.Is(err, ErrSystemUpdateMutationGrantConflict) {
		t.Fatalf("mismatched binding consume = %v", err)
	}
	if _, _, err := updates.ConsumeSystemUpdateMutationGrant(t.Context(), job.ID, issued.GrantToken, claim.LeaseGeneration+1, binding, base.Add(3*time.Second)); !errors.Is(err, ErrSystemUpdateMutationGrantConflict) {
		t.Fatalf("mismatched lease generation consume = %v", err)
	}
	consumed, replayed, err := updates.ConsumeSystemUpdateMutationGrant(t.Context(), job.ID, issued.GrantToken, claim.LeaseGeneration, binding, base.Add(3*time.Second))
	if err != nil || replayed || consumed.ConsumedAt == nil {
		t.Fatalf("first consume = %#v replayed=%v err=%v", consumed, replayed, err)
	}
	firstConsumedAt := *consumed.ConsumedAt
	replayedGrant, replayed, err := updates.ConsumeSystemUpdateMutationGrant(t.Context(), job.ID, issued.GrantToken, claim.LeaseGeneration, binding, base.Add(4*time.Second))
	if err != nil || !replayed || replayedGrant.ConsumedAt == nil || !replayedGrant.ConsumedAt.Equal(firstConsumedAt) {
		t.Fatalf("exact response-loss replay = %#v replayed=%v err=%v", replayedGrant, replayed, err)
	}
	if _, _, err := updates.ConsumeSystemUpdateMutationGrant(t.Context(), job.ID, issued.GrantToken, claim.LeaseGeneration, wrong, base.Add(4*time.Second)); !errors.Is(err, ErrSystemUpdateMutationGrantConflict) {
		t.Fatalf("consumed grant with different binding replay = %v", err)
	}
}

func TestMemorySystemUpdateMutationGrantRejectsReplayAfterGrantOrJobInvalidation(t *testing.T) {
	base := time.Date(2026, 7, 19, 12, 30, 0, 0, time.UTC)
	t.Run("expired after consume", func(t *testing.T) {
		updates, job, claim := prepareMutationGrantInstallingJob(t, base, time.Minute)
		binding := validMutationGrantBinding()
		issued, err := updates.IssueSystemUpdateMutationGrant(t.Context(), job.ID, IssueSystemUpdateMutationGrantParams{
			AgentServiceID: "updater-central", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Binding: binding,
		}, base.Add(2*time.Second), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := updates.ConsumeSystemUpdateMutationGrant(t.Context(), job.ID, issued.GrantToken, claim.LeaseGeneration, binding, base.Add(3*time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := updates.ConsumeSystemUpdateMutationGrant(t.Context(), job.ID, issued.GrantToken, claim.LeaseGeneration, binding, issued.Grant.ExpiresAt); !errors.Is(err, ErrSystemUpdateMutationGrantConflict) {
			t.Fatalf("expired consumed grant replay = %v", err)
		}
	})

	t.Run("terminal after consume", func(t *testing.T) {
		updates, job, claim := prepareMutationGrantInstallingJob(t, base.Add(time.Minute), time.Minute)
		binding := validMutationGrantBinding()
		issued, err := updates.IssueSystemUpdateMutationGrant(t.Context(), job.ID, IssueSystemUpdateMutationGrantParams{
			AgentServiceID: "updater-central", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Binding: binding,
		}, base.Add(time.Minute+2*time.Second), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := updates.ConsumeSystemUpdateMutationGrant(t.Context(), job.ID, issued.GrantToken, claim.LeaseGeneration, binding, base.Add(time.Minute+3*time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := updates.ReportSystemUpdateJob(t.Context(), job.ID, SystemUpdateReport{
			AgentServiceID: "updater-central", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration,
			Sequence: claim.ReportSequence + 1, Status: SystemUpdateStatusSucceeded, Progress: 100,
		}, base.Add(time.Minute+4*time.Second), time.Minute); err != nil {
			t.Fatal(err)
		}
		if _, _, err := updates.ConsumeSystemUpdateMutationGrant(t.Context(), job.ID, issued.GrantToken, claim.LeaseGeneration, binding, base.Add(time.Minute+5*time.Second)); !errors.Is(err, ErrSystemUpdateMutationGrantConflict) {
			t.Fatalf("terminal consumed grant replay = %v", err)
		}
	})

	t.Run("old lease generation after consume", func(t *testing.T) {
		caseBase := base.Add(2 * time.Minute)
		updates, job, claim := prepareMutationGrantInstallingJob(t, caseBase, time.Minute)
		binding := validMutationGrantBinding()
		issued, err := updates.IssueSystemUpdateMutationGrant(t.Context(), job.ID, IssueSystemUpdateMutationGrantParams{
			AgentServiceID: "updater-central", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Binding: binding,
		}, caseBase.Add(2*time.Second), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := updates.ConsumeSystemUpdateMutationGrant(t.Context(), job.ID, issued.GrantToken, claim.LeaseGeneration, binding, caseBase.Add(3*time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-central", "host-01", job.ID, map[string]string{"worker-01": "systemd"}, caseBase.Add(4*time.Second), time.Minute); err != nil {
			t.Fatal(err)
		}
		if _, _, err := updates.ConsumeSystemUpdateMutationGrant(t.Context(), job.ID, issued.GrantToken, claim.LeaseGeneration, binding, caseBase.Add(5*time.Second)); !errors.Is(err, ErrSystemUpdateMutationGrantConflict) {
			t.Fatalf("old-generation consumed grant replay = %v", err)
		}
	})
}

func TestMemorySystemUpdateMutationGrantRejectsExpiredAndOldLease(t *testing.T) {
	base := time.Date(2026, 7, 19, 13, 0, 0, 0, time.UTC)
	t.Run("expired", func(t *testing.T) {
		updates, job, claim := prepareMutationGrantInstallingJob(t, base, time.Minute)
		issued, err := updates.IssueSystemUpdateMutationGrant(t.Context(), job.ID, IssueSystemUpdateMutationGrantParams{
			AgentServiceID: "updater-central", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration,
			Binding: validMutationGrantBinding(),
		}, base.Add(2*time.Second), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := updates.ConsumeSystemUpdateMutationGrant(t.Context(), job.ID, issued.GrantToken, claim.LeaseGeneration, validMutationGrantBinding(), issued.Grant.ExpiresAt); !errors.Is(err, ErrSystemUpdateMutationGrantConflict) {
			t.Fatalf("expired grant consume = %v", err)
		}
	})

	t.Run("old lease generation", func(t *testing.T) {
		updates, job, claim := prepareMutationGrantInstallingJob(t, base, time.Minute)
		issued, err := updates.IssueSystemUpdateMutationGrant(t.Context(), job.ID, IssueSystemUpdateMutationGrantParams{
			AgentServiceID: "updater-central", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration,
			Binding: validMutationGrantBinding(),
		}, base.Add(2*time.Second), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-central", "host-01", job.ID, map[string]string{"worker-01": "systemd"}, base.Add(3*time.Second), time.Minute); err != nil {
			t.Fatal(err)
		}
		if _, _, err := updates.ConsumeSystemUpdateMutationGrant(t.Context(), job.ID, issued.GrantToken, claim.LeaseGeneration, validMutationGrantBinding(), base.Add(4*time.Second)); !errors.Is(err, ErrSystemUpdateMutationGrantConflict) {
			t.Fatalf("old-generation grant consume = %v", err)
		}
	})
}

func TestMemorySystemUpdateMutationGrantConcurrentConsumeIsExactlyOnce(t *testing.T) {
	base := time.Date(2026, 7, 19, 14, 0, 0, 0, time.UTC)
	updates, job, claim := prepareMutationGrantInstallingJob(t, base, time.Minute)
	binding := validMutationGrantBinding()
	issued, err := updates.IssueSystemUpdateMutationGrant(t.Context(), job.ID, IssueSystemUpdateMutationGrantParams{
		AgentServiceID: "updater-central", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Binding: binding,
	}, base.Add(2*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 24
	start := make(chan struct{})
	var first, replay, failures atomic.Int32
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, replayed, err := updates.ConsumeSystemUpdateMutationGrant(t.Context(), job.ID, issued.GrantToken, claim.LeaseGeneration, binding, base.Add(3*time.Second))
			if err != nil {
				failures.Add(1)
			} else if replayed {
				replay.Add(1)
			} else {
				first.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if failures.Load() != 0 || first.Load() != 1 || replay.Load() != workers-1 {
		t.Fatalf("concurrent consume first=%d replay=%d failures=%d", first.Load(), replay.Load(), failures.Load())
	}
}

func TestMemorySystemUpdateMutationGrantValidatesOperationPlanAndSession(t *testing.T) {
	base := time.Date(2026, 7, 19, 15, 0, 0, 0, time.UTC)
	updates, job, claim := prepareMutationGrantInstallingJob(t, base, time.Minute)
	baseParams := IssueSystemUpdateMutationGrantParams{
		AgentServiceID: "updater-central", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration,
		Binding: validMutationGrantBinding(),
	}
	tests := []struct {
		name   string
		mutate func(*IssueSystemUpdateMutationGrantParams)
		want   error
	}{
		{name: "wrong operation for state", mutate: func(p *IssueSystemUpdateMutationGrantParams) {
			p.Binding.Operation = SystemUpdateMutationOperationReconcile
		}, want: ErrSystemUpdateAuthorizationState},
		{name: "uppercase plan digest", mutate: func(p *IssueSystemUpdateMutationGrantParams) { p.Binding.PlanSHA256 = strings.Repeat("A", 64) }, want: ErrInvalidSystemUpdate},
		{name: "short session", mutate: func(p *IssueSystemUpdateMutationGrantParams) { p.Binding.SessionID = "short" }, want: ErrInvalidSystemUpdate},
		{name: "session whitespace", mutate: func(p *IssueSystemUpdateMutationGrantParams) { p.Binding.SessionID = "session bad value" }, want: ErrInvalidSystemUpdate},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params := baseParams
			test.mutate(&params)
			_, err := updates.IssueSystemUpdateMutationGrant(t.Context(), job.ID, params, base.Add(2*time.Second), time.Minute)
			if !errors.Is(err, test.want) {
				t.Fatalf("issue error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestMemorySystemUpdateMutationGrantRequiresExactOperationState(t *testing.T) {
	base := time.Date(2026, 7, 19, 16, 0, 0, 0, time.UTC)
	updates, job, claim := prepareMutationGrantInstallingJob(t, base, time.Minute)
	if _, _, err := updates.ReportSystemUpdateJob(t.Context(), job.ID, SystemUpdateReport{
		AgentServiceID: "updater-central", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration,
		Sequence: claim.ReportSequence + 1, Status: SystemUpdateStatusReconciling, Progress: 80,
	}, base.Add(2*time.Second), time.Minute); err != nil {
		t.Fatal(err)
	}

	apply := validMutationGrantBinding()
	params := IssueSystemUpdateMutationGrantParams{
		AgentServiceID: "updater-central", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Binding: apply,
	}
	if _, err := updates.IssueSystemUpdateMutationGrant(t.Context(), job.ID, params, base.Add(3*time.Second), time.Minute); !errors.Is(err, ErrSystemUpdateAuthorizationState) {
		t.Fatalf("apply grant in reconciling state = %v", err)
	}

	reconcile := apply
	reconcile.Operation = SystemUpdateMutationOperationReconcile
	reconcile.SessionID = "session-reconcile-01"
	params.Binding = reconcile
	issued, err := updates.IssueSystemUpdateMutationGrant(t.Context(), job.ID, params, base.Add(3*time.Second), time.Minute)
	if err != nil {
		t.Fatalf("reconcile grant in reconciling state = %v", err)
	}
	if _, replayed, err := updates.ConsumeSystemUpdateMutationGrant(t.Context(), job.ID, issued.GrantToken, claim.LeaseGeneration, reconcile, base.Add(4*time.Second)); err != nil || replayed {
		t.Fatalf("reconcile grant consume replayed=%v err=%v", replayed, err)
	}
}

func prepareMutationGrantInstallingJob(t *testing.T, base time.Time, executionLeaseTTL time.Duration) (*MemorySystemUpdateStore, SystemUpdateJob, SystemUpdateClaim) {
	t.Helper()
	updates := NewMemorySystemUpdateStore()
	job, _, err := updates.CreateSystemUpdateJob(t.Context(), CreateSystemUpdateJobParams{
		TargetID: "worker-01", TargetServiceType: "worker", AgentServiceID: "updater-central", ExecutionHostID: "host-01",
		DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0",
		Strategy: SystemUpdateStrategyWhenIdle, IdempotencyKey: "mutation-grant-" + base.Format("150405"), RequestedByUserID: "user-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-central", "host-01", "", map[string]string{"worker-01": "systemd"}, base, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := updates.ReportSystemUpdateJob(t.Context(), job.ID, SystemUpdateReport{
		AgentServiceID: "updater-central", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration,
		Sequence: claim.ReportSequence, Status: SystemUpdateStatusInstalling, Progress: 65,
	}, base.Add(time.Second), executionLeaseTTL); err != nil {
		t.Fatal(err)
	}
	return updates, job, claim
}

func validMutationGrantBinding() SystemUpdateMutationGrantBinding {
	return SystemUpdateMutationGrantBinding{
		HostID: "host-01", TargetID: "worker-01", TargetVersion: "v1.1.0", DeploymentMode: "systemd",
		Operation: SystemUpdateMutationOperationApply, PlanSHA256: strings.Repeat("a", 64), SessionID: "session-apply-0001",
	}
}
