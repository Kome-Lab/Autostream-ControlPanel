package store

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMemorySystemUpdateJobSnapshotsExecutionHostOwnership(t *testing.T) {
	ctx := t.Context()
	updates := NewMemorySystemUpdateStore()
	ownership, err := updates.SwitchSystemUpdateExecutionHost(
		ctx,
		"host-a",
		0,
		SystemUpdateTransportPullV2,
		"updater-host-a",
		7,
	)
	if err != nil {
		t.Fatal(err)
	}

	job := createOwnershipFenceJob(t, updates, "worker-a", "updater-host-a", "host-a", "snapshot-a")
	if job.TransportMode != ownership.TransportMode ||
		job.OwnershipEpoch != ownership.OwnershipEpoch ||
		job.PolicyRevision != ownership.PolicyRevision {
		t.Fatalf("job ownership snapshot = %#v, ownership=%#v", job, ownership)
	}

	if _, _, err := updates.CreateSystemUpdateJob(ctx, CreateSystemUpdateJobParams{
		TargetID:          "worker-wrong-owner",
		TargetServiceType: "worker",
		AgentServiceID:    "updater-central",
		ExecutionHostID:   "host-a",
		DeploymentMode:    "systemd",
		CurrentVersion:    "v1.0.0",
		TargetVersion:     "v1.1.0",
		Strategy:          SystemUpdateStrategyWhenIdle,
		IdempotencyKey:    "wrong-owner",
		RequestedByUserID: "admin",
	}); !errors.Is(err, ErrSystemUpdateOwnershipConflict) {
		t.Fatalf("wrong owner create err = %v", err)
	}
}

func TestMemorySystemUpdateUnownedHostRejectsJob(t *testing.T) {
	updates := NewMemorySystemUpdateStore()
	if _, _, err := updates.CreateSystemUpdateJob(t.Context(), CreateSystemUpdateJobParams{
		TargetID: "worker-unowned", TargetServiceType: "worker",
		AgentServiceID: "updater-unowned", ExecutionHostID: "host-unowned",
		DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0",
		Strategy: SystemUpdateStrategyWhenIdle, IdempotencyKey: "unowned-host",
		RequestedByUserID: "admin",
	}); !errors.Is(err, ErrSystemUpdateOwnershipConflict) {
		t.Fatalf("unowned host create err = %v", err)
	}
}

func TestMemorySystemUpdateHostATokenCannotClaimHostB(t *testing.T) {
	ctx := t.Context()
	updates := NewMemorySystemUpdateStore()
	for _, fixture := range []struct {
		hostID  string
		agentID string
	}{
		{hostID: "host-a", agentID: "updater-host-a"},
		{hostID: "host-b", agentID: "updater-host-b"},
	} {
		if _, err := updates.SwitchSystemUpdateExecutionHost(
			ctx,
			fixture.hostID,
			0,
			SystemUpdateTransportPullV2,
			fixture.agentID,
			1,
		); err != nil {
			t.Fatal(err)
		}
	}
	createOwnershipFenceJob(t, updates, "worker-b", "updater-host-b", "host-b", "host-b-job")

	if _, _, err := updates.ClaimSystemUpdateJob(
		ctx,
		"updater-host-a",
		"host-b",
		"",
		map[string]string{"worker-b": "systemd"},
		time.Now().UTC(),
		time.Minute,
	); !errors.Is(err, ErrSystemUpdateOwnershipConflict) {
		t.Fatalf("host A token claim host B err = %v", err)
	}
}

func TestMemorySystemUpdatePullReportAndGrantRequireAuthenticatedHost(t *testing.T) {
	ctx := t.Context()
	base := time.Now().UTC()
	updates := NewMemorySystemUpdateStore()
	for _, hostID := range []string{"host-a", "host-b"} {
		if _, err := updates.SwitchSystemUpdateExecutionHost(
			ctx,
			hostID,
			0,
			SystemUpdateTransportPullV2,
			"updater-shared",
			1,
		); err != nil {
			t.Fatal(err)
		}
	}
	ownership, err := updates.GetSystemUpdateExecutionHost(ctx, "host-b")
	if err != nil {
		t.Fatal(err)
	}
	job := createOwnershipFenceJob(t, updates, "worker-b", "updater-shared", "host-b", "host-bound-runtime")
	claim, _, err := updates.ClaimSystemUpdateJob(
		ctx,
		"updater-shared",
		"host-b",
		"",
		map[string]string{"worker-b": "systemd"},
		base,
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	report := SystemUpdateReport{
		AgentServiceID:  "updater-shared",
		ExecutionHostID: "host-a",
		LeaseToken:      claim.LeaseToken,
		LeaseGeneration: claim.LeaseGeneration,
		Sequence:        claim.ReportSequence,
		Status:          SystemUpdateStatusInstalling,
		Progress:        70,
	}
	if _, _, err := updates.ReportSystemUpdateJob(ctx, job.ID, report, base.Add(time.Second), time.Minute); !errors.Is(err, ErrSystemUpdateOwnershipConflict) {
		t.Fatalf("cross-host pull report err = %v", err)
	}
	report.ExecutionHostID = "host-b"
	if _, _, err := updates.ReportSystemUpdateJob(ctx, job.ID, report, base.Add(time.Second), time.Minute); err != nil {
		t.Fatal(err)
	}
	binding := SystemUpdateMutationGrantBinding{
		HostID:         "host-b",
		TransportMode:  ownership.TransportMode,
		OwnershipEpoch: ownership.OwnershipEpoch,
		PolicyRevision: ownership.PolicyRevision,
		TargetID:       "worker-b",
		TargetVersion:  "v1.1.0",
		DeploymentMode: "systemd",
		Operation:      SystemUpdateMutationOperationApply,
		PlanSHA256:     strings.Repeat("b", 64),
		SessionID:      "cross-host-grant-session",
	}
	if _, err := updates.IssueSystemUpdateMutationGrant(ctx, job.ID, IssueSystemUpdateMutationGrantParams{
		AgentServiceID:  "updater-shared",
		ExecutionHostID: "host-a",
		LeaseToken:      claim.LeaseToken,
		LeaseGeneration: claim.LeaseGeneration,
		Binding:         binding,
	}, base.Add(2*time.Second), time.Minute); !errors.Is(err, ErrSystemUpdateOwnershipConflict) {
		t.Fatalf("cross-host pull grant err = %v", err)
	}
}

func TestMemorySystemUpdateExplicitOwnershipAllowsOneSplitBrainClaimant(t *testing.T) {
	ctx := t.Context()
	updates := NewMemorySystemUpdateStore()
	if _, err := updates.SwitchSystemUpdateExecutionHost(
		ctx,
		"host-a",
		0,
		SystemUpdateTransportPullV2,
		"updater-host-a",
		1,
	); err != nil {
		t.Fatal(err)
	}
	createOwnershipFenceJob(t, updates, "worker-a", "updater-host-a", "host-a", "split-brain")

	start := make(chan struct{})
	type result struct {
		agent string
		err   error
	}
	results := make(chan result, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, agentID := range []string{"updater-host-a", "updater-central"} {
		agentID := agentID
		go func() {
			ready.Done()
			<-start
			_, _, err := updates.ClaimSystemUpdateJob(
				ctx,
				agentID,
				"host-a",
				"",
				map[string]string{"worker-a": "systemd"},
				time.Now().UTC(),
				time.Minute,
			)
			results <- result{agent: agentID, err: err}
		}()
	}
	ready.Wait()
	close(start)

	for range 2 {
		result := <-results
		switch result.agent {
		case "updater-host-a":
			if result.err != nil {
				t.Fatalf("pull owner claim err = %v", result.err)
			}
		case "updater-central":
			if !errors.Is(result.err, ErrSystemUpdateOwnershipConflict) {
				t.Fatalf("central split-brain claim err = %v", result.err)
			}
		}
	}
}

func TestMemorySystemUpdateOwnershipDriftRejectsReportAuthorizeAndGrantReplay(t *testing.T) {
	ctx := t.Context()
	base := time.Now().UTC()
	updates := NewMemorySystemUpdateStore()
	ownership, err := updates.SwitchSystemUpdateExecutionHost(
		ctx,
		"host-a",
		0,
		SystemUpdateTransportPullV2,
		"updater-host-a",
		11,
	)
	if err != nil {
		t.Fatal(err)
	}
	job := createOwnershipFenceJob(t, updates, "worker-a", "updater-host-a", "host-a", "drift")
	claim, _, err := updates.ClaimSystemUpdateJob(
		ctx,
		"updater-host-a",
		"host-a",
		"",
		map[string]string{"worker-a": "systemd"},
		base,
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := updates.ReportSystemUpdateJob(ctx, job.ID, SystemUpdateReport{
		AgentServiceID:  "updater-host-a",
		ExecutionHostID: "host-a",
		LeaseToken:      claim.LeaseToken,
		LeaseGeneration: claim.LeaseGeneration,
		Sequence:        claim.ReportSequence,
		Status:          SystemUpdateStatusInstalling,
		Progress:        70,
	}, base.Add(time.Second), time.Minute); err != nil {
		t.Fatal(err)
	}
	binding := SystemUpdateMutationGrantBinding{
		HostID:         "host-a",
		TargetID:       "worker-a",
		TargetVersion:  "v1.1.0",
		DeploymentMode: "systemd",
		Operation:      SystemUpdateMutationOperationApply,
		PlanSHA256:     strings.Repeat("a", 64),
		SessionID:      "ownership-drift-session-01",
		TransportMode:  ownership.TransportMode,
		OwnershipEpoch: ownership.OwnershipEpoch,
		PolicyRevision: ownership.PolicyRevision,
	}
	issued, err := updates.IssueSystemUpdateMutationGrant(ctx, job.ID, IssueSystemUpdateMutationGrantParams{
		AgentServiceID:  "updater-host-a",
		ExecutionHostID: "host-a",
		LeaseToken:      claim.LeaseToken,
		LeaseGeneration: claim.LeaseGeneration,
		Binding:         binding,
	}, base.Add(2*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	updates.mu.Lock()
	drifted := updates.executionHosts["host-a"]
	drifted.OwnershipEpoch++
	drifted.PolicyRevision++
	drifted.UpdatedAt = base.Add(3 * time.Second)
	updates.executionHosts["host-a"] = drifted
	updates.mu.Unlock()

	if _, _, err := updates.ReportSystemUpdateJob(ctx, job.ID, SystemUpdateReport{
		AgentServiceID:  "updater-host-a",
		ExecutionHostID: "host-a",
		LeaseToken:      claim.LeaseToken,
		LeaseGeneration: claim.LeaseGeneration,
		Sequence:        claim.ReportSequence + 1,
		Status:          SystemUpdateStatusSucceeded,
		Progress:        100,
	}, base.Add(4*time.Second), time.Minute); !errors.Is(err, ErrSystemUpdateOwnershipConflict) {
		t.Fatalf("stale report err = %v", err)
	}
	if err := updates.AuthorizeSystemUpdateMutation(ctx, job.ID, SystemUpdateAuthorization{
		AgentServiceID:  "updater-host-a",
		ExecutionHostID: "host-a",
		LeaseToken:      claim.LeaseToken,
		LeaseGeneration: claim.LeaseGeneration,
		TargetID:        "worker-a",
		TargetVersion:   "v1.1.0",
		DeploymentMode:  "systemd",
	}, base.Add(4*time.Second)); !errors.Is(err, ErrSystemUpdateOwnershipConflict) {
		t.Fatalf("stale authorization err = %v", err)
	}
	if _, err := updates.IssueSystemUpdateMutationGrant(ctx, job.ID, IssueSystemUpdateMutationGrantParams{
		AgentServiceID:  "updater-host-a",
		ExecutionHostID: "host-a",
		LeaseToken:      claim.LeaseToken,
		LeaseGeneration: claim.LeaseGeneration,
		Binding:         binding,
	}, base.Add(4*time.Second), time.Minute); !errors.Is(err, ErrSystemUpdateOwnershipConflict) {
		t.Fatalf("stale grant issue err = %v", err)
	}
	if _, _, err := updates.ConsumeSystemUpdateMutationGrant(
		ctx,
		job.ID,
		issued.GrantToken,
		claim.LeaseGeneration,
		binding,
		base.Add(4*time.Second),
	); !errors.Is(err, ErrSystemUpdateMutationGrantConflict) {
		t.Fatalf("stale grant replay err = %v", err)
	}
}

func createOwnershipFenceJob(
	t *testing.T,
	updates *MemorySystemUpdateStore,
	targetID, agentID, hostID, key string,
) SystemUpdateJob {
	t.Helper()
	job, created, err := updates.CreateSystemUpdateJob(t.Context(), CreateSystemUpdateJobParams{
		TargetID:          targetID,
		TargetServiceType: "worker",
		AgentServiceID:    agentID,
		ExecutionHostID:   hostID,
		DeploymentMode:    "systemd",
		CurrentVersion:    "v1.0.0",
		TargetVersion:     "v1.1.0",
		Strategy:          SystemUpdateStrategyWhenIdle,
		IdempotencyKey:    key,
		RequestedByUserID: "admin",
	})
	if err != nil || !created {
		t.Fatalf("create job %s: created=%v err=%v", targetID, created, err)
	}
	return job
}
